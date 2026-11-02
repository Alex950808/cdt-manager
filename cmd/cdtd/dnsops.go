package main

// DNS 巡检层：登记对齐、目标解析（server / group→当班）、单条同步、
// 全量巡检（免同步短路 + 失败冷却 + 去重告警）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cdt-manager/cloudflare"
	"cdt-manager/ddns"
)

const dnsRetryCooldown = 120 * time.Second

// DNSOps 依赖 store（登记/设置）与 engine（当班判定）。
type DNSOps struct {
	store  *Store
	engine *Engine
}

func NewDNSOps(store *Store, engine *Engine) *DNSOps {
	return &DNSOps{store: store, engine: engine}
}

// cfClient 按设置构造 Cloudflare 客户端；token 为空返回 nil（= DDNS 未配置）。
func (d *DNSOps) cfClient() *cloudflare.Client {
	cfg := d.store.Settings()
	switch cfg["ddns_cf_auth_mode"] {
	case "global_key":
		if cfg["ddns_cf_email"] == "" || cfg["ddns_cf_key"] == "" {
			return nil
		}
		return cloudflare.New("", cloudflare.WithGlobalKey(cfg["ddns_cf_email"], cfg["ddns_cf_key"]))
	default:
		if cfg["ddns_cf_token"] == "" {
			return nil
		}
		return cloudflare.New(cfg["ddns_cf_token"])
	}
}

// SyncRegistry 把设置里的记录名同步进登记表（设置里删掉的就删掉）。
func (d *DNSOps) SyncRegistry() error {
	cfg := d.store.Settings()
	var names []string
	if err := json.Unmarshal([]byte(cfg["ddns_records"]), &names); err != nil {
		names = nil
	}
	keep := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		keep[n] = true
		if err := d.store.EnsureDNSRecord(n); err != nil {
			return err
		}
	}
	for _, r := range d.store.DNSRecords() {
		if !keep[r.Record] {
			if err := d.store.DeleteDNSRecord(r.Record); err != nil {
				return err
			}
		}
	}
	return nil
}

// targetInfo 解析绑定目标的当前状态与 IP：
// server → 自身实时快照；group → 当班机（actual_duty，failover/manual 感知）快照。
func (d *DNSOps) targetInfo(kind, targetID string) (status, ip string, running bool, dutyLabel string) {
	switch kind {
	case "server":
		inst, ok := d.engine.instanceByID(targetID)
		if !ok {
			return "", "", false, ""
		}
		snap, ok := d.store.Snapshot(inst.ID)
		if !ok {
			return "Unknown", "", false, inst.Name
		}
		return snap.Status, snap.PublicIP, snap.Status == "Running", inst.Name
	case "group":
		g, ok := d.store.GroupGet(targetID)
		if !ok {
			return "", "", false, ""
		}
		duty, has := d.engine.actualDuty(g, time.Now())
		if !has {
			return "", "", false, g.Name
		}
		inst, ok := d.engine.instanceByID(duty)
		if !ok {
			return "", "", false, g.Name
		}
		snap, ok := d.store.Snapshot(inst.ID)
		if !ok {
			return "Unknown", "", false, inst.Name
		}
		return snap.Status, snap.PublicIP, snap.Status == "Running", inst.Name
	}
	return "", "", false, ""
}

// SyncTarget 把某目标的全部绑定记录同步到 ip；ip 为空时自算（desired_ip 规则）。
// 返回每条记录的状态文案（调用方 combineStatuses 汇总）。
func (d *DNSOps) SyncTarget(kind, targetID, ip string, force bool) []string {
	cfg := d.store.Settings()
	s := d.syncer(cfg)
	var statuses []string
	for _, rec := range d.store.DNSRecords() {
		if rec.TargetKind != kind || rec.TargetID != targetID {
			continue
		}
		statuses = append(statuses, d.syncRecord(cfg, s, rec, ip, force))
	}
	return statuses
}

// syncRecord 单条同步：desired_ip 判定 → ddns.Sync → 结果落库。
func (d *DNSOps) syncRecord(cfg map[string]string, s *ddns.Syncer, rec DNSRecord, ip string, force bool) string {
	if ip == "" {
		status, liveIP, running, _ := d.targetInfo(rec.TargetKind, rec.TargetID)
		if rec.TargetKind == "" {
			d.store.UpdateDNSResult(rec.Record, "", "⏸ 未绑定", time.Now().Unix(), true)
			return "⏸ 未绑定"
		}
		if !running && !transientStatus[status] {
			st := "⏸ 目标未运行"
			if running {
				st = "⚠️ 目标没有公网 IP"
			}
			d.store.UpdateDNSResult(rec.Record, rec.LastIP, st, time.Now().Unix(), true)
			return st
		}
		ip = liveIP
		if !ddns.IsPublicIPv4(ip) {
			st := "⚠️ 目标没有公网 IP"
			d.store.UpdateDNSResult(rec.Record, rec.LastIP, st, time.Now().Unix(), true)
			return st
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ttl := settingInt(cfg, "ddns_ttl", 60)
	result := s.Sync(ctx, ddns.Target{
		Enabled:    cfg["ddns_enabled"] == "1",
		Record:     rec.Record,
		ZoneID:     cfg["ddns_zone_id"],
		TTL:        ttl,
		Proxied:    cfg["ddns_proxied"] == "1",
		LastIP:     rec.LastIP,
		LastStatus: rec.LastStatus,
	}, ip, force)

	switch {
	case strings.HasPrefix(result.Status, "✅"):
		// 成功：IP 或状态有变化才写库（last_at = 上次真正同步时间）
		if force || rec.LastIP != ip || rec.LastStatus != result.Status {
			d.store.UpdateDNSResult(rec.Record, ip, result.Status, time.Now().Unix(), false)
		}
		d.store.FlagClear("dns_retry:" + rec.Record)
		d.store.FlagClearPrefix("dns_fail:" + rec.Record + ":")
	default:
		// 失败：保留上次成功 IP，只更新状态
		d.store.UpdateDNSResult(rec.Record, "", result.Status, time.Now().Unix(), true)
	}
	return result.Status
}

func (d *DNSOps) syncer(cfg map[string]string) *ddns.Syncer {
	return &ddns.Syncer{CF: d.cfClient()}
}

// Reconcile 全量巡检：登记对齐 → 逐条绑定记录判定与同步。
func (d *DNSOps) Reconcile(cfg map[string]string) {
	if cfg["ddns_enabled"] != "1" {
		return
	}
	if err := d.SyncRegistry(); err != nil {
		return
	}
	s := d.syncer(cfg)

	for _, rec := range d.store.DNSRecords() {
		if rec.TargetKind == "" {
			continue // 未绑定的记录一律不动
		}
		status, liveIP, running, label := d.targetInfo(rec.TargetKind, rec.TargetID)
		_ = status
		if !running {
			continue // 目标没在跑：不动解析、不改状态
		}
		if !ddns.IsPublicIPv4(liveIP) {
			continue
		}
		// 免同步短路：解析已正确且上次成功
		if rec.LastIP == liveIP && strings.HasPrefix(rec.LastStatus, "✅") {
			continue
		}
		// 冷却：刚失败过 120s 内不重试（force 手动同步不受限）
		if age := d.store.FlagAge("dns_retry:" + rec.Record); age >= 0 && age < dnsRetryCooldown {
			continue
		}
		before := rec.LastIP
		resultStatus := d.syncRecord(cfg, s, rec, liveIP, false)
		if strings.HasPrefix(resultStatus, "✅") {
			if before != liveIP && !d.store.HasRunningEventKind("shift_done", "manual", "group_new") {
				d.store.AddEvent(Event{Kind: EvDNS, Status: "done",
					Title:  fmt.Sprintf("%s：解析已更新", rec.Record),
					Detail: fmt.Sprintf("%s → %s（%s）", orDash(before), liveIP, label)})
			}
		} else if strings.HasPrefix(resultStatus, "❌") || strings.HasPrefix(resultStatus, "⚠️") {
			// 同类失败去重告警；冷却起点每次失败刷新（按注释意图修正原实现偏差）
			failKey := "dns_fail:" + rec.Record + ":" + resultStatus
			if d.store.FlagAge(failKey) < 0 {
				d.store.FlagSet(failKey)
				d.store.AddEvent(Event{Kind: EvError, Status: "error",
					Title: rec.Record + "：" + resultStatus})
			}
			d.store.FlagSet("dns_retry:" + rec.Record)
		}
	}
}

// TestDDNS 连通性自检（只读不改记录）。
func (d *DNSOps) TestDDNS() (string, string) {
	cfg := d.store.Settings()
	if cfg["ddns_enabled"] != "1" {
		return "⏸ DDNS 未启用", ""
	}
	var names []string
	_ = json.Unmarshal([]byte(cfg["ddns_records"]), &names)
	if len(names) == 0 {
		return "❌ DDNS 异常", "记录名未配置"
	}
	c := d.cfClient()
	if c == nil {
		return "❌ DDNS 异常", "Cloudflare Token 未配置"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	zoneID := cfg["ddns_zone_id"]
	if zoneID == "" {
		id, err := c.ZoneID(ctx, ddns.RootDomain(names[0]))
		if err != nil || id == "" {
			return "❌ DDNS 异常", "无法识别 Zone，请手动填写 Zone ID"
		}
		zoneID = id
	}
	var missing []string
	for _, n := range names {
		rec, err := c.FindRecord(ctx, zoneID, "A", strings.TrimSpace(n))
		if err != nil || rec == nil {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return "❌ 获取 ID 失败", strings.Join(missing, "、") + " 未找到 A 记录"
	}
	return "✅ 连接正常", "记录均就绪"
}
