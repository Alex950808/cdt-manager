package main

// 引擎数据模型：换班组 / 保活 / 事件 / 运行标记。

import (
	"strconv"
	"strings"
	"time"
)

// GroupMember 是换班组的一个成员（有序轮换列表，无主备概念——
// 每个时刻恰好一台"当班"，其余"待机"）。
type GroupMember struct {
	InstanceID string `json:"instance_id"`          // 指向 Instance.ID
	Position   int    `json:"position"`             // 轮换顺序，从 0 起
	Hours      int    `json:"hours"`                // day 周期：当班时长（小时）；时刻表模式下可留空
	Days       int    `json:"days"`                 // month 周期：当班天数
	StartTime  string `json:"start_time,omitempty"` // 时刻表模式："HH:MM"
	EndTime    string `json:"end_time,omitempty"`   // "HH:MM"；"00:00" 表达到午夜
}

// GroupRun 是一个班次的运行状态（原 shift_runs）。
type GroupRun struct {
	Slot            string    `json:"slot"`     // 班次开始时间字符串（跨班次即新 run）
	Phase           string    `json:"phase"`    // pending/switching/settled/failover/manual/breaker/error
	Stage           string    `json:"stage"`    // "" / starting_duty / stopping_standby
	EventID         int64     `json:"event_id"` // 本班次进行中事件
	DutyID          string    `json:"duty_id"`  // failover/manual 覆盖的当班机
	StandbyIDs      []string  `json:"standby_ids"`
	Failover        bool      `json:"failover"`
	FailoverReason  string    `json:"failover_reason"` // traffic | stall
	FailoverFrom    string    `json:"failover_from"`
	Manual          bool      `json:"manual"`
	Origin          string    `json:"origin"` // "" | new_group
	DNSBefore       string    `json:"dns_before,omitempty"`
	Attempts        int       `json:"attempts"`
	StartedAt       time.Time `json:"started_at"` // 超时计时锚点
	Note            string    `json:"note"`
	Settled         bool      `json:"settled"`   // 已结算（first_settle 完成后 true）
	Recovered       bool      `json:"recovered"` // 抢占恢复事件待收尾
	NotifiedTimeout bool      `json:"notified_timeout"`
}

// Group 是一个换班组。
type Group struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Enabled         bool          `json:"enabled"`
	FailoverEnabled bool          `json:"failover_enabled"`
	Cycle           string        `json:"cycle"`     // "day" | "month"
	AnchorAt        time.Time     `json:"anchor_at"` // hours 轮换锚点（成员变更时重置）
	Members         []GroupMember `json:"members"`
	Run             *GroupRun     `json:"run,omitempty"` // 当前班次状态
}

// ActiveMembers 返回启用成员（day 看 hours>0；month 看 days>0 或全员 days==0 平摊），按 position 排序。
func (g *Group) ActiveMembers() []GroupMember {
	anyDays := false
	if g.Cycle == "month" {
		for _, m := range g.Members {
			if m.Days > 0 {
				anyDays = true
				break
			}
		}
	}
	var out []GroupMember
	for _, m := range g.Members {
		if g.Cycle == "month" && !anyDays && m.Days >= 0 {
			out = append(out, m)
			continue
		}
		if !memberEnabled(g.Cycle, m) {
			continue
		}
		out = append(out, m)
	}
	// 稳定按 position 排序
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Position < out[j-1].Position; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func memberEnabled(cycle string, m GroupMember) bool {
	if cycle == "month" {
		return m.Days > 0
	}
	return m.Hours > 0 || hasValidTimeWindow(m)
}

// hasValidTimeWindow 成员是否填写了成对的合法时刻（时刻表模式）。
func hasValidTimeWindow(m GroupMember) bool {
	s, okS := parseHHMM(m.StartTime)
	e, okE := parseHHMM(m.EndTime)
	if !okS || !okE {
		return false
	}
	return e == 0 || e > s // end=0 表达到午夜；同日段要求 end>start
}

// parseHHMM 解析 "HH:MM" 为当日分钟数；"00:00" 返回 0（合法，可能表达到午夜）。
func parseHHMM(s string) (int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 24 || m < 0 || m > 59 {
		return 0, false
	}
	if h == 24 && m != 0 {
		return 0, false
	}
	return h*60 + m, true
}

// Keepalive 是单机保活目标：目标机"停了就拉起来"，不做换班。
type Keepalive struct {
	InstanceID string `json:"instance_id"`
	Enabled    bool   `json:"enabled"`
}

// Sample 是一条流量采样点（曲线用；空读数不落样本防插假 0）。
type Sample struct {
	At time.Time `json:"at"`
	GB float64   `json:"gb"`
}

// 引擎阶段常量。
const (
	PhasePending   = "pending"
	PhaseSwitching = "switching"
	PhaseSettled   = "settled"
	PhaseFailover  = "failover"
	PhaseManual    = "manual"
	PhaseBreaker   = "breaker"
	PhaseError     = "error"

	StageStartingDuty    = "starting_duty"
	StageStoppingStandby = "stopping_standby"
)

// 状态集合。
var transientStatus = map[string]bool{
	"Starting": true, "Pending": true, "Stopping": true,
}

// 已物理停机的判定：StopCharging/KeepCharging/Stop/Not-applicable 都算停到位。
func isStoppedAnyMode(status, stoppedMode string) bool {
	if status != "Stopped" {
		return false
	}
	switch stoppedMode {
	case "StopCharging", "KeepCharging", "Stop", "Not-applicable", "":
		return true
	}
	return false
}

// overThreshold 流量超限判定：threshold>0 且 used>=threshold（含等号）。
// used 未知（nil）永不超限。
func overThreshold(used *float64, threshold float64) bool {
	return threshold > 0 && used != nil && *used >= threshold
}

// Event 事件模型。
type Event struct {
	ID            int64     `json:"id"`
	Kind          string    `json:"kind"`
	Title         string    `json:"title"`
	Detail        string    `json:"detail,omitempty"`
	Status        string    `json:"status"`                   // running / done / error
	Progress      []string  `json:"progress,omitempty"`       // ≤50 条，相邻去重
	PendingStatus string    `json:"pending_status,omitempty"` // 非空 = 待确认器确认
	PendingKind   string    `json:"pending_kind,omitempty"`   // keepalive / manual / ""
	ServerID      string    `json:"server_id,omitempty"`
	At            time.Time `json:"at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// 事件 kind 常量。
const (
	EvStart         = "start"
	EvShiftDone     = "shift_done"
	EvManual        = "manual"
	EvRecovery      = "recovery"
	EvGroupNew      = "group_new"
	EvGroupDissolve = "group_dissolve"
	EvFailover      = "failover"
	EvShiftFailover = "shift_failover"
	EvLimitStop     = "limit_stop"
	EvBreaker       = "breaker"
	EvDNS           = "dns"
	EvBilling       = "billing"
	EvDailyReport   = "daily_report"
	EvError         = "error"
	EvWarning       = "warning"
)

// 引擎核心常量。
const (
	breakerStopCooldown = 120 * time.Second // 熔断/超限停机指令节流
	ddnsWaitSeconds     = 180 * time.Second // DDNS 失败等待上限
	eventConfirmTimeout = 600 * time.Second // 事件确认超时（保活除外）
	fastInterval        = 10 * time.Second  // 快速对账间隔
	fastWindow          = 90 * time.Second  // 快速对账窗口时长
	billTZOffset        = 8 * 3600          // 计费月判定固定 UTC+8
	eventProgressMax    = 50                // 单事件进度封顶，丢最旧
)

// keepaliveMilestones：保活重试进度只在里程碑次数追加进度行。
var keepaliveMilestones = map[int]bool{3: true, 10: true, 30: true, 100: true, 300: true, 1000: true, 3000: true}
