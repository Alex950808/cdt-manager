package main

// 调度器：主对账循环 + 事件确认循环（间隔钳制、快速窗口、确认超时、日报、清理）。

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// StartScheduler 启动两个后台循环（进程生命周期内运行）。
func (s *Server) StartScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	s.schedCancel = cancel

	go func() { // 主循环：对账 → DNS 巡检 → 账单刷新 → 日报
		for {
			interval := s.reconcileInterval()
			s.engine.ReconcileAll(ctx)
			if ctx.Err() != nil {
				return
			}
			cfg := s.store.Settings()
			s.dnsOps.Reconcile(cfg)
			s.billingRefreshDue(ctx, cfg)
			s.maybeDailyReport(cfg)
			s.pruneOld()
			// 快速对账窗口：10s；否则等用户配置间隔（1 秒切片便于退出）
			wait := interval
			if s.engine.fastActive() {
				wait = fastInterval
			}
			deadline := time.Now().Add(wait)
			for time.Now().Before(deadline) && ctx.Err() == nil {
				time.Sleep(time.Second)
			}
		}
	}()

	go func() { // 事件确认循环：每 5s 扫 running + pending 的事件
		for {
			s.confirmPendingEvents(ctx)
			time.Sleep(5 * time.Second)
			if ctx.Err() != nil {
				return
			}
		}
	}()
}

// StopScheduler 停止后台循环。
func (s *Server) StopScheduler() {
	if s.schedCancel != nil {
		s.schedCancel()
	}
}

// reconcileInterval 对账间隔（钳制 30–3600 秒）。
func (s *Server) reconcileInterval() time.Duration {
	sec := settingInt(s.store.Settings(), "reconcile_interval", 60)
	if sec < 30 {
		sec = 30
	}
	if sec > 3600 {
		sec = 3600
	}
	return time.Duration(sec) * time.Second
}

// confirmPendingEvents 事件确认器：查真实状态，到位收尾，超时报错。
func (s *Server) confirmPendingEvents(ctx context.Context) {
	for _, ev := range s.store.RunningEvents() {
		if ev.PendingStatus == "" {
			continue
		}
		// keepalive 特例：不设超时；被移出保活则收尾
		if ev.PendingKind == "keepalive" {
			if !s.store.KeepaliveHas(ev.ServerID) {
				s.store.EventFinish(ev.ID, "error", "", "已移出单机保活，停止拉起")
				s.store.FlagClear("keepalive_tries:" + ev.ServerID)
				continue
			}
		} else if time.Since(ev.UpdatedAt) > eventConfirmTimeout {
			s.store.EventFinish(ev.ID, "error", "", "超时未确认（600s）")
			continue
		}

		inst, ok := s.engine.instanceByID(ev.ServerID)
		if !ok {
			s.store.EventFinish(ev.ID, "error", "", "缺少待确认目标")
			continue
		}
		info, err := s.clientFor(&inst).DescribeInstance(ctx, inst.RegionID, inst.InstanceID)
		if err != nil {
			s.store.EventAppendProgress(ev.ID, "状态查询失败，下轮再试")
			continue
		}
		// 刷快照
		if snap, ok := s.store.Snapshot(inst.ID); ok {
			snap.Status, snap.StoppedMode, snap.PublicIP = info.Status, info.StoppedMode, info.PublicIP
			snap.RefreshedAt = time.Now()
			s.store.PutSnapshot(inst.ID, &snap)
		}
		if !statusReached(ev.PendingStatus, info.Status) {
			continue // 未到位，下一轮
		}
		// 到位：进度首条
		s.store.EventAppendProgress(ev.ID, "已确认 "+statusText(info.Status))

		// 按 pending_kind 收尾
		switch ev.PendingKind {
		case "keepalive":
			cfg := s.store.Settings()
			if cfg["ddns_enabled"] == "1" && info.PublicIP != "" {
				statuses := s.dnsOps.SyncTarget("server", inst.ID, info.PublicIP, false)
				if line := combineStatuses(statuses); !strings.HasPrefix(line, "⏸") {
					s.store.EventAppendProgress(ev.ID, "DDNS："+line)
				}
			}
			s.store.EventFinish(ev.ID, "done", fmt.Sprintf("%s：保活拉起完成", inst.Name), "")
			s.store.FlagClear("keepalive_tries:" + inst.ID)
			cfg2 := s.store.Settings()
			if cfg2["notify_on_keepalive"] == "1" {
				go s.sendNotify(cfg2, "keepalive", inst.Name+"：抢占恢复",
					"实例疑被抢占，已自动恢复运行。", ev.ID)
			}
		case "manual":
			cfg := s.store.Settings()
			if cfg["ddns_enabled"] == "1" && info.PublicIP != "" && ev.PendingStatus == "Running" {
				statuses := s.dnsOps.SyncTarget("server", inst.ID, info.PublicIP, false)
				if line := combineStatuses(statuses); !strings.HasPrefix(line, "⏸") {
					s.store.EventAppendProgress(ev.ID, "DDNS："+line)
				}
			}
			s.store.EventFinish(ev.ID, "done", "", "")
		default:
			s.store.EventFinish(ev.ID, "done", "", "")
		}
	}
}

// statusReached 目标状态是否达成（目标 Stopped 不强求节省停机模式）。
func statusReached(target, status string) bool {
	return status == target
}

func statusText(s string) string {
	if s == "" {
		return "未知"
	}
	return s
}

// billingRefreshDue 按 TTL 刷新账单/余额（TTL 内零请求；跨月立即刷）。
func (s *Server) billingRefreshDue(ctx context.Context, cfg map[string]string) {
	ttl := time.Duration(settingInt(cfg, "billing_interval", 600)) * time.Second
	if ttl < time.Minute {
		ttl = time.Minute
	}
	cycle := time.Now().Format("2006-01")
	for _, inst := range s.store.List() {
		snap, ok := s.store.Snapshot(inst.ID)
		if ok && time.Since(snap.RefreshedAt) < ttl && snap.BillCycle == cycle {
			continue
		}
		s.refreshOne(inst) // 复用现有刷新（状态/流量/余额/账单并行）
	}
}

// maybeDailyReport 每日报告（默认 23:58 UTC+8，到点即发 + flag 去重）。
func (s *Server) maybeDailyReport(cfg map[string]string) {
	if cfg["daily_report_enabled"] != "1" {
		return
	}
	now := billTime(time.Now())
	hm := now.Format("15:04")
	if hm < "23:58" {
		return
	}
	date := now.Format("2006-01-02")
	key := "daily_report:" + date
	if s.store.FlagAge(key) >= 0 {
		return
	}
	e := s.engine
	var b strings.Builder
	var lines []string
	for _, inst := range s.store.List() {
		snap, _ := s.store.Snapshot(inst.ID)
		used := "—"
		if snap.TrafficGB != nil {
			used = fmt.Sprintf("%.1f/%.0fGB", *snap.TrafficGB, inst.Quota())
		}
		status := snap.Status
		if status == "" {
			status = "未知"
		}
		lines = append(lines, fmt.Sprintf("%s：%s，流量 %s，余额 %.2f %s，账单 %.2f",
			inst.Name, statusText(status), used, snap.Balance, orDash(snap.Currency), snap.BillTotal))
	}
	sort.Strings(lines)
	b.WriteString(strings.Join(lines, "\n"))
	e.notifyIf(cfg, "daily_report_enabled", "每日流量报告（"+date+"）", b.String(), "daily_report")
	s.store.FlagSet(key)
}

// sendNotify 发通知并把结果写进指定事件（最后一条进度）。
func (s *Server) sendNotify(cfg map[string]string, kind, title, text string, eventID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results := notifySendAll(ctx, cfg, buildMessage(kind, title, text))
	if line := notifyResultText(results, "通知"); line != "" && eventID != 0 {
		s.store.EventAppendProgress(eventID, line)
	}
}

// pruneOld 定时清理：事件 14 天、flags 90 天（每小时由主循环调用一次）。
func (s *Server) pruneOld() {
	if s.store.pruneCursor.Add(time.Hour).After(time.Now()) {
		return
	}
	s.store.pruneCursor = time.Now()
	s.store.PruneOld(time.Now().Add(-14*24*time.Hour), time.Now().Add(-90*24*time.Hour))
}
