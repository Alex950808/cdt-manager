package main

import (
	"sort"
	"strings"
)

// --- 系统设置项 ---------------------------------
//
// 设置即键值表：DDNS、通知、引擎阈值都从这里读。引擎每轮取快照，
// 界面每项保存整体覆盖。

var settingDefaults = map[string]string{
	"reconcile_interval":   "60",  // 对账间隔秒（最小 30，防止打爆 API 配额）
	"billing_interval":     "600", // 账单缓存 TTL 秒（最小 60）
	"shift_timeout_min":    "10",  // 换班超时（分钟）
	"duty_stall_seconds":   "180", // 当班机迟迟不就绪的容忍秒数
	"ddns_enabled":         "0",
	"ddns_records":         "[]",    // JSON 数组：登记的记录名（记录名唯一真源）
	"ddns_cf_auth_mode":    "token", // token | global_key
	"ddns_cf_token":        "",
	"ddns_cf_email":        "", // global_key 模式
	"ddns_cf_key":          "", // global_key 模式
	"ddns_zone_id":         "", // 空 = 自动识别
	"ddns_ttl":             "60",
	"ddns_proxied":         "0",
	"notify_tg_enabled":    "0",
	"notify_tg_token":      "",
	"notify_tg_chat_id":    "",
	"notify_wh_enabled":    "0",
	"notify_wh_url":        "",
	"notify_wh_method":     "POST",
	"notify_wh_headers":    "",
	"notify_wh_body":       "",
	"notify_mail_enabled":  "0",
	"notify_mail_host":     "",
	"notify_mail_port":     "465",
	"notify_mail_username": "",
	"notify_mail_password": "",
	"notify_mail_to":       "",
	"notify_mail_security": "ssl",
	"notify_on_shift":      "0",
	"notify_on_failover":   "0",
	"notify_on_recovery":   "0",
	"notify_on_breaker":    "0",
	"notify_on_limit_stop": "0",
	"notify_on_error":      "0",
	"notify_on_keepalive":  "0",
	"daily_report_enabled": "0",
	"brand":                "CDT 管理台",
}

// Settings 返回设置副本（默认值合并）。
func (s *Store) Settings() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(settingDefaults)+len(s.data.Settings))
	for k, v := range settingDefaults {
		out[k] = v
	}
	for k, v := range s.data.Settings {
		out[k] = v
	}
	return out
}

// SetSettings 整体覆盖指定键（未提及的键保持原值）。
func (s *Store) SetSettings(patch map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Settings == nil {
		s.data.Settings = map[string]string{}
	}
	for k, v := range patch {
		if _, known := settingDefaults[k]; !known {
			continue // 未知键拒绝，防止拼错悄悄失效
		}
		s.data.Settings[k] = strings.TrimSpace(v)
	}
	return s.save()
}

// --- DNS 记录登记与绑定 -------------------------------------------------------
//
// 「绑定必须是显式的」：target_kind 为空的记录一律不被任何自动流程触碰。
// 记录名唯一真源是设置里的 ddns_records（JSON 数组），表随 sync_registry 对齐。

type DNSRecord struct {
	Record     string `json:"record"`      // 记录名（FQDN），主键
	TargetKind string `json:"target_kind"` // "" | "server" | "group"
	TargetID   string `json:"target_id"`
	LastIP     string `json:"last_ip"` // 上次成功同步到的 IP
	LastStatus string `json:"last_status"`
	LastAt     int64  `json:"last_at"` // 上次真正同步的时间戳
	SortOrder  int    `json:"sort_order"`
}

// DNSRecords 返回按排序的登记记录副本。
func (s *Store) DNSRecords() []DNSRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DNSRecord, len(s.data.DNSRecords))
	copy(out, s.data.DNSRecords)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].Record < out[j].Record
	})
	return out
}

// EnsureDNSRecord 登记记录名（已存在则不动）。
func (s *Store) EnsureDNSRecord(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.data.DNSRecords {
		if r.Record == name {
			return nil
		}
	}
	s.data.DNSRecords = append(s.data.DNSRecords, DNSRecord{Record: name})
	return s.save()
}

// DeleteDNSRecord 移除登记（设置里删掉的名字由 sync_registry 调此清理）。
func (s *Store) DeleteDNSRecord(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.data.DNSRecords[:0]
	found := false
	for _, r := range s.data.DNSRecords {
		if r.Record == name {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return nil
	}
	s.data.DNSRecords = kept
	return s.save()
}

// GetDNSRecord 取单条登记。
func (s *Store) GetDNSRecord(name string) (DNSRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.data.DNSRecords {
		if r.Record == name {
			return r, true
		}
	}
	return DNSRecord{}, false
}

// BindDNSRecord 显式绑定记录到目标；kind 为空表示解绑并清空展示状态
// （避免旧 IP 被误当"当前解析"）。
func (s *Store) BindDNSRecord(record, kind, targetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.data.DNSRecords {
		if r.Record != record {
			continue
		}
		if kind == "" {
			s.data.DNSRecords[i].TargetKind = ""
			s.data.DNSRecords[i].TargetID = ""
			s.data.DNSRecords[i].LastIP = ""
			s.data.DNSRecords[i].LastStatus = ""
		} else {
			s.data.DNSRecords[i].TargetKind = kind
			s.data.DNSRecords[i].TargetID = targetID
		}
		return s.save()
	}
	return errNotFound("记录未登记: " + record)
}

// UpdateDNSResult 落同步结果。keepIP=true 表示失败场景保留旧 IP（界面要能
// 看出"当前还指向旧地址"），只更新状态文案。
func (s *Store) UpdateDNSResult(record, ip, status string, at int64, keepIP bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.data.DNSRecords {
		if r.Record != record {
			continue
		}
		if !keepIP {
			s.data.DNSRecords[i].LastIP = ip
		}
		s.data.DNSRecords[i].LastStatus = status
		s.data.DNSRecords[i].LastAt = at
		_ = s.save()
		return
	}
}

// UnbindAllFor 清除指向某目标的所有绑定（删除实例/换班组时调用）。
func (s *Store) UnbindAllFor(kind, targetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i, r := range s.data.DNSRecords {
		if r.TargetKind == kind && r.TargetID == targetID {
			s.data.DNSRecords[i].TargetKind = ""
			s.data.DNSRecords[i].TargetID = ""
			s.data.DNSRecords[i].LastIP = ""
			s.data.DNSRecords[i].LastStatus = ""
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}
