package ddns

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"cdt-manager/cloudflare"
)

// 结果状态文案：✅=成功、⚠️/❌=失败、⏸=没做事（不算失败）。
// SYNCED 与 UNCHANGED 文案相同，区分靠 Result.Changed。
const (
	StatusSynced       = "✅ 已同步"
	StatusInvalidIP    = "⚠️ IP 无效跳过"
	StatusNoRecordID   = "❌ 获取 ID 失败"
	StatusUpdateFailed = "❌ 更新失败"
	StatusDisabled     = "⏸ DDNS 未启用"
	StatusError        = "❌ DDNS 异常"
)

// Result 是一次同步的结果。
type Result struct {
	Status  string
	Detail  string
	Changed bool
	IP      string
}

func (r Result) String() string { return r.Status }

// Target 是一次同步所需的全部输入（含 per-record 的上次同步结果，
// 供"IP 未变化"零请求短路使用——每条记录独立，不能共用全局值）。
type Target struct {
	Enabled    bool
	Record     string // A 记录名（FQDN）
	ZoneID     string // 手动 Zone ID；空 = 按记录名后两段自动识别
	TTL        int    // 0 → 60
	Proxied    bool
	LastIP     string // 上次成功同步到的 IP
	LastStatus string // 上次同步结果文案
}

// Syncer 执行同步。CF 为 nil 时所有同步返回 Disabled。
type Syncer struct {
	CF  *cloudflare.Client
	Now func() time.Time // 可注入时钟（重试间隔用）
}

// IsPublicIPv4 仅接受公网 IPv4：解析失败 / IPv6 / 私网 / 回环 / 链路本地 /
// 未指定 / 组播一律拒绝。IPv6 一律不接受（DDNS 只同步 A 记录）。
func IsPublicIPv4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil {
		return false
	}
	return !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsUnspecified() || ip.IsMulticast())
}

// RootDomain 取记录名的最后两段作为根域（ddns.example.com → example.com；
// a.b.example.com → example.com）。已在手动 Zone ID 场景下不会被调用。
func RootDomain(record string) string {
	parts := strings.Split(strings.TrimSuffix(strings.TrimSpace(record), "."), ".")
	if len(parts) < 2 {
		return strings.TrimSpace(record)
	}
	return parts[len(parts)-2] + "." + parts[len(parts)-1]
}

// Sync 把记录指向 ip。前置校验任一命中即返回（不发请求）；
// 配置/Zone 类错误不进重试循环；网络/查询类错误重试 3 次、间隔 1s。
func (s *Syncer) Sync(ctx context.Context, tc Target, ip string, force bool) Result {
	ip = strings.TrimSpace(ip)
	if !tc.Enabled {
		return Result{Status: StatusDisabled, IP: ip}
	}
	if tc.Record == "" {
		return Result{Status: StatusError, Detail: "记录名未配置", IP: ip}
	}
	if !IsPublicIPv4(ip) {
		return Result{Status: StatusInvalidIP, Detail: "IP=" + ip, IP: ip}
	}
	// 零请求短路：上次就是同步到这个 IP 且成功，跳过（force 时仍执行）
	if !force && tc.LastIP == ip && strings.HasPrefix(tc.LastStatus, "✅") {
		return Result{Status: StatusSynced, Detail: "IP 未变化", Changed: false, IP: ip}
	}
	if s.CF == nil {
		return Result{Status: StatusDisabled, Detail: "Cloudflare 客户端未配置", IP: ip}
	}

	ttl := tc.TTL
	if ttl <= 0 {
		ttl = 60
	}
	zoneID := tc.ZoneID
	if zoneID == "" {
		root := RootDomain(tc.Record)
		id, err := s.CF.ZoneID(ctx, root)
		if err != nil || id == "" {
			return Result{Status: StatusError,
				Detail: fmt.Sprintf("无法自动识别 %s 的 Zone ID，请手动填写", root), IP: ip}
		}
		zoneID = id
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		rec, err := s.CF.FindRecord(ctx, zoneID, "A", tc.Record)
		if err != nil {
			lastErr = err
		} else if rec == nil {
			lastErr = fmt.Errorf("记录不存在或查询失败")
		} else {
			err := s.CF.UpdateRecord(ctx, zoneID, rec.ID, cloudflare.Record{
				Type: "A", Name: tc.Record, Content: ip, TTL: ttl, Proxied: tc.Proxied,
			})
			if err == nil {
				return Result{Status: StatusSynced,
					Detail: tc.Record + " → " + ip, Changed: true, IP: ip}
			}
			lastErr = err
		}
		if attempt < 2 {
			now := time.Now
			if s.Now != nil {
				now = s.Now
			}
			select {
			case <-ctx.Done():
				return Result{Status: StatusError, Detail: ctx.Err().Error(), IP: ip}
			case <-time.After(1 * time.Second):
				_ = now
			}
		}
	}

	detail := fmt.Sprint(lastErr)
	switch {
	case strings.Contains(detail, "Zone ID"):
		return Result{Status: StatusError, Detail: detail, IP: ip}
	case strings.Contains(detail, "记录"):
		return Result{Status: StatusNoRecordID, Detail: detail, IP: ip}
	default:
		return Result{Status: StatusUpdateFailed, Detail: detail, IP: ip}
	}
}
