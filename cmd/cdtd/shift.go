package main

// 换班组引擎：排班判定、超限转移（failover）、熔断（breaker）、限额停机、
// 手动换班、新建组引导与解散、结算与超时。

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"cdt-manager/alicloud"
	"time"
)

// --- 排班判定 ------------------------------------------------------------------

// dutyAtAt 时钟答案：当前时刻谁当班。
// hours 模式：anchor 起按各成员 hours 累加循环取模；
// month 模式：当月 1 号（UTC+8）起按 position 铺 days，最后一人排到月末。
func (e *Engine) dutyAt(g Group, t time.Time) string {
	members := g.ActiveMembers()
	if len(members) == 0 {
		return ""
	}
	if g.Cycle == "month" {
		return dutyAtMonth(members, t)
	}
	if isTimetableGroup(members) {
		return dutyAtTimetable(members, t)
	}
	anchor := g.AnchorAt
	if anchor.IsZero() {
		anchor = time.Now().Add(-time.Hour) // 兜底：无锚点时回退一小时起算
	}
	if !t.After(anchor) {
		return members[0].InstanceID
	}
	total := 0
	for _, m := range members {
		total += m.Hours
	}
	if total <= 0 {
		return members[0].InstanceID
	}
	offset := int(t.Sub(anchor).Seconds()) % (total * 3600)
	for _, m := range members {
		span := m.Hours * 3600
		if offset < span {
			return m.InstanceID
		}
		offset -= span
	}
	return members[0].InstanceID
}

// dutyAtMonth 月周期：按当月日序铺段（天数全为 0 时按人数平摊 31 天、余数给末位）。
func dutyAtMonth(members []GroupMember, t time.Time) string {
	day := billDayOfMonth(t) // UTC+8 的当月第几天，1 起
	spans := monthLayoutDays(members)
	covered := 0
	for i, m := range members {
		span := spans[i]
		if span <= 0 {
			span = 1 // 防御：段长至少 1 天
		}
		if day >= covered+1 && day <= covered+span {
			return m.InstanceID
		}
		covered += span
	}
	// 超出排班总天数（短月）→ 最后一位兜底
	return members[len(members)-1].InstanceID
}

// isTimetableGroup day 周期下全部启用成员都填了成对合法时刻 → 时刻表模式。
func isTimetableGroup(members []GroupMember) bool {
	if len(members) < 2 {
		return false
	}
	for _, m := range members {
		if !hasValidTimeWindow(m) {
			return false
		}
	}
	return true
}

// dutyAtTimetable 时刻表模式：组时区（UTC+8）的当日分钟数落在谁的 [start,end)。
// end==0 表达到午夜；跨天段（end<start）按 [start,1440)∪[0,end) 判定。
func dutyAtTimetable(members []GroupMember, t time.Time) string {
	cur := billTime(t).Hour()*60 + billTime(t).Minute()
	for _, m := range members {
		s, _ := parseHHMM(m.StartTime)
		e, _ := parseHHMM(m.EndTime)
		if e == 0 {
			e = 1440
		}
		if s < e {
			if cur >= s && cur < e {
				return m.InstanceID
			}
		} else { // 跨天段
			if cur >= s || cur < e {
				return m.InstanceID
			}
		}
	}
	// 时刻表未覆盖的时刻 → 第一位兜底
	return members[0].InstanceID
}

// monthLayoutDays 启用成员 days 全为 0 时按人数平摊 31 天（余数给最后一位）。
func monthLayoutDays(members []GroupMember) []int {
	out := make([]int, len(members))
	anySet := false
	for i, m := range members {
		out[i] = m.Days
		if m.Days > 0 {
			anySet = true
		}
	}
	if anySet {
		return out
	}
	base := 31 / len(members)
	rem := 31 - base*len(members)
	for i := range out {
		out[i] = base
		if i == len(out)-1 {
			out[i] += rem
		}
	}
	return out
}

// slotFor 班次键（跨班次即新 run）。
func (e *Engine) slotFor(g Group, t time.Time) string {
	members := g.ActiveMembers()
	if g.Cycle == "month" {
		return billDate(t) // 月周期按自然日切班（代班仅当日有效）
	}
	if isTimetableGroup(members) {
		// 时刻表：班次键 = 段起始日 + 段开始时刻（跨天段的起点在昨天）
		cur := billTime(t).Hour()*60 + billTime(t).Minute()
		for _, m := range members {
			s, _ := parseHHMM(m.StartTime)
			e, _ := parseHHMM(m.EndTime)
			if e == 0 {
				e = 1440
			}
			switch {
			case s < e && cur >= s && cur < e:
				return billDate(t) + "#" + strconv.Itoa(s)
			case s >= e && cur >= s:
				return billDate(t) + "#" + strconv.Itoa(s)
			case s >= e && cur < e:
				return billDate(t.AddDate(0, 0, -1)) + "#" + strconv.Itoa(s)
			}
		}
		return billDate(t)
	}
	// hours：当前班次的起点时间 = anchor + n*总时长
	if len(members) == 0 {
		return "empty"
	}
	anchor := g.AnchorAt
	if anchor.IsZero() {
		anchor = time.Now().Add(-time.Hour)
	}
	total := 0
	for _, m := range members {
		total += m.Hours
	}
	if total <= 0 {
		return "empty"
	}
	elapsed := int(t.Sub(anchor).Seconds())
	cycle := total * 3600
	within := ((elapsed % cycle) + cycle) % cycle
	// 累加已消费的时段：命中的班次起点 = anchor + 之前成员时长之和
	// （此前直接返回 anchor + n*cycle，跨入下一班后起点不推进——换班永不触发）
	consumed := 0
	for _, m := range members {
		span := m.Hours * 3600
		if within < span {
			return anchor.Add(time.Duration((elapsed/cycle)*cycle+consumed) * time.Second).Format(time.RFC3339)
		}
		within -= span
		consumed += span
	}
	return t.Format("2006-01-02")
}

// actualDuty 实际当班 = 时钟答案被 run.failover / run.manual 覆盖；
// 被指定的机器已不在启用成员中则回退时钟答案。
func (e *Engine) actualDuty(g Group, t time.Time) (string, bool) {
	duty := e.dutyAt(g, t)
	run := g.Run
	if run != nil && run.DutyID != "" && (run.Failover || run.Manual) {
		valid := false
		for _, m := range g.ActiveMembers() {
			if m.InstanceID == run.DutyID {
				valid = true
				break
			}
		}
		if valid {
			duty = run.DutyID
		}
	}
	if duty == "" {
		return "", false
	}
	if _, ok := e.instanceByID(duty); !ok {
		return "", false
	}
	return duty, true
}

// --- 组对账主流程 ----------------------------------------------------------------

// reconcileGroup 换班组一轮对账（决策链：超限转移 → 熔断 / 限额停机 → stall 转移 → 正常推进）。
func (e *Engine) reconcileGroup(ctx context.Context, cfg map[string]string, g Group) error {
	if !g.Enabled {
		return nil
	}
	t := time.Now()
	label := groupName(g)

	// 1) 月周期隔天复位：仅 stall 型代班清掉（traffic 不复位、manual 不复位）
	if g.Cycle == "month" {
		e.resetMonthDayFailover(&g, t)
	}

	// 2) 计算当班与 slot；载入/初始化 run
	slot := e.slotFor(g, t)
	duty, hasDuty := e.actualDuty(g, t)
	if !hasDuty {
		return nil // 排不出当班（成员<2 等）：不动作
	}
	run := g.Run
	if run != nil && run.Slot != slot {
		// 班次更替：收尾旧班次的关联事件（防止跨班次后 manual/start 事件成孤儿）
		if run.EventID != 0 {
			e.store.EventFinish(run.EventID, "done", "", "班次已更替")
		}
		run = &GroupRun{Slot: slot, Phase: PhasePending, StartedAt: t}
		g.Run = run
		_ = e.store.GroupSave(g)
		e.closeOrphanShiftEvents(g)
	}
	if run == nil {
		run = &GroupRun{Slot: slot, Phase: PhasePending, StartedAt: t}
		g.Run = run
		_ = e.store.GroupSave(g)
	}

	// 3) 全员刷新；当班读不到 → 本轮直接 return（不发任何指令）
	dutyInst, ok := e.instanceByID(duty)
	if !ok {
		return nil
	}
	standbyIDs := standbyIDsOf(g, duty)
	states := map[string]engInstance{}
	states[duty] = e.refresh(ctx, cfg, dutyInst)
	if !states[duty].live {
		key := "duty_err:" + g.ID + ":" + slot
		if e.store.FlagAge(key) < 0 {
			e.store.FlagSet(key)
			e.store.AddEvent(Event{Kind: EvError, Status: "error",
				Title: fmt.Sprintf("%s：当班机 %s 状态读取失败，本轮跳过", label, dutyInst.Name)})
		}
		return nil
	}
	allReadable := true
	for _, sid := range standbyIDs {
		inst, ok := e.instanceByID(sid)
		if !ok {
			continue
		}
		states[sid] = e.refresh(ctx, cfg, inst)
		if !states[sid].live {
			allReadable = false
			key := "member_err:" + sid
			if e.store.FlagAge(key) < 0 {
				e.store.FlagSet(key)
				e.store.AddEvent(Event{Kind: EvError, Status: "error",
					Title: fmt.Sprintf("%s：成员 %s 状态读取失败", label, inst.Name)})
			}
		}
	}

	// 4) 决策链（按序短路）
	if overThreshold(states[duty].TrafficGB, dutyInst.Threshold()) {
		if g.FailoverEnabled {
			cand := e.pickCandidate(g, run, states, duty)
			if cand == "" {
				if !allReadable {
					e.startBreaker(ctx, cfg, g, run, states, "其余成员状态读取失败")
				} else {
					e.startBreaker(ctx, cfg, g, run, states, "全部成员流量超限")
				}
				return nil
			}
			e.startFailover(ctx, cfg, g, run, duty, cand, "traffic", states)
			// 当班已切换：同步局部视图后继续正常推进
			duty = cand
			dutyInst, _ = e.instanceByID(duty)
			standbyIDs = standbyIDsOf(g, duty)
			if _, ok := states[duty]; !ok {
				states[duty] = e.refresh(ctx, cfg, dutyInst)
			}
		} else {
			e.handleGroupLimitStop(ctx, cfg, g, run, dutyInst, states[duty])
			return nil
		}
	} else if states[duty].Status != "Running" && !run.Manual && !run.Failover {
		// 当班起不来（stall）：首次置计时（本轮照常尝试拉起），超过容忍秒数才转移
		stallKey := "duty_stall:" + g.ID + ":" + slot
		if e.store.FlagAge(stallKey) < 0 {
			e.store.FlagSet(stallKey)
		}
		stallSecs := settingInt(cfg, "duty_stall_seconds", 180)
		if e.store.FlagAge(stallKey) >= time.Duration(stallSecs)*time.Second {
			if g.FailoverEnabled {
				cand := e.pickCandidate(g, run, states, duty)
				if cand == "" {
					if e.store.FlagAge("stall_nocand:"+g.ID+":"+slot) < 0 {
						e.store.FlagSet("stall_nocand:" + g.ID + ":" + slot)
						e.store.AddEvent(Event{Kind: EvError, Status: "error",
							Title: fmt.Sprintf("%s：当班起不来且无可接班成员", label)})
					}
					return nil
				}
				e.store.FlagSet("failover_tried:" + g.ID + ":" + slot + ":" + duty)
				e.startFailover(ctx, cfg, g, run, duty, cand, "stall", states)
				// stall 转移后本轮先不推进：原当班可能还在 Starting，
				// 下一轮走 ensureSwitch 的强制停机例外路径
				return nil
			} else if e.store.FlagAge("stall_disabled:"+g.ID+":"+slot) < 0 {
				e.store.FlagSet("stall_disabled:" + g.ID + ":" + slot)
				e.store.AddEvent(Event{Kind: EvWarning, Status: "done",
					Title: fmt.Sprintf("%s：当班起不来，但组未启用故障转移，保持等待", label)})
			}
		}
	} else {
		e.store.FlagClear("duty_stall:" + g.ID + ":" + slot)
	}

	// 5) 正常推进
	e.ensureSwitch(ctx, cfg, g, run, duty, states, dutyInst, standbyIDs, label)
	return nil
}

// standbyIDsOf 排除当班后的启用成员（待机列表）。
func standbyIDsOf(g Group, duty string) []string {
	var out []string
	for _, m := range g.ActiveMembers() {
		if m.InstanceID != duty {
			out = append(out, m.InstanceID)
		}
	}
	return out
}

// pickCandidate 在健康、未超限、本班次未试过的待机里挑流量最小者。
func (e *Engine) pickCandidate(g Group, run *GroupRun, states map[string]engInstance, exclude string) string {
	var best string
	var bestUsed float64
	for sid, st := range states {
		if sid == exclude {
			continue
		}
		if !st.live {
			continue
		}
		if e.store.FlagAge("failover_tried:"+g.ID+":"+run.Slot+":"+sid) >= 0 {
			continue
		}
		inst, ok := e.instanceByID(sid)
		if !ok || overThreshold(st.TrafficGB, inst.Threshold()) {
			continue
		}
		used := 0.0
		if st.TrafficGB != nil {
			used = *st.TrafficGB
		}
		if best == "" || used < bestUsed {
			best, bestUsed = sid, used
		}
	}
	return best
}

// startFailover 发起一次故障转移：原当班降为待机（会被停机），候选接管。
func (e *Engine) startFailover(ctx context.Context, cfg map[string]string, g Group, run *GroupRun,
	from, to, reason string, states map[string]engInstance) {

	e.store.FlagSet("failover_tried:" + g.ID + ":" + run.Slot + ":" + from)
	run.Failover = true
	run.FailoverReason = reason
	run.FailoverFrom = from
	run.DutyID = to
	run.Phase = PhaseSwitching
	run.Stage = ""
	run.StartedAt = time.Now() // 超时重新计时
	run.Settled = false
	_ = e.store.GroupSave(g)

	// 复用本班次 running 事件改题，不另开新事件
	evID := e.ensureShiftEvent(g, run)
	e.store.EventUpdate(evID, fmt.Sprintf("%s：故障转移（%s）→ %s",
		groupName(g), failoverReasonText(reason), e.instName(to)))
	e.store.EventAppendProgress(evID, fmt.Sprintf("原当班 %s %s，由 %s 接管",
		e.instName(from), failoverReasonText(reason), e.instName(to)))
}

func failoverReasonText(reason string) string {
	if reason == "stall" {
		return "起不来"
	}
	return "流量超限"
}

// startBreaker 熔断：当班超限且无可接班 → 全组停机 + 通知一次（需确认到位）。
func (e *Engine) startBreaker(ctx context.Context, cfg map[string]string, g Group, run *GroupRun,
	states map[string]engInstance, reason string) {

	label := groupName(g)
	slot := run.Slot
	// 进行中的换班事件以 error 收尾
	if run.EventID != 0 {
		e.store.EventFinish(run.EventID, "error", "",
			fmt.Sprintf("%s，触发全局熔断", reason))
		run.EventID = 0
	}
	// 全组逐台停（120s 节流；已物理停机不重发）
	allStopped := true
	for sid, st := range states {
		if !st.live {
			allStopped = false // 读不到的机器会一直"看起来在跑"，靠节流兜底
			continue
		}
		inst, ok := e.instanceByID(sid)
		if !ok || isStoppedAnyMode(st.Status, st.StoppedMode) {
			continue
		}
		cmdKey := fmt.Sprintf("breaker_stop:%s:%s:%s", g.ID, slot, sid)
		if age := e.store.FlagAge(cmdKey); age >= 0 && age < breakerStopCooldown {
			allStopped = false
			continue
		}
		e.store.FlagSet(cmdKey)
		if _, err := e.cloud.StopInstance(ctx, &inst, alicloud.StoppedModeStopCharging, true); err != nil {
			e.store.FlagSet("breaker_stop_err:" + g.ID + ":" + slot + ":" + sid)
			allStopped = false
		}
	}
	// run 置熔断态
	run.Phase = PhaseBreaker
	run.EventID = 0
	run.Note = reason
	_ = e.store.GroupSave(g)

	// 通知：本 slot 一次 + 全员确认停机到位
	if cfg["notify_on_breaker"] == "1" && e.store.FlagAge("breaker:"+g.ID+":"+slot) < 0 && allStopped {
		e.store.FlagSet("breaker:" + g.ID + ":" + slot)
		e.store.AddEvent(Event{Kind: EvBreaker, Status: "error",
			Title: fmt.Sprintf("%s：触发全局熔断，全组已停机", label)})
		e.notifyIf(cfg, "notify_on_breaker", label+"：全局熔断",
			fmt.Sprintf("当班流量超限（%s），无可接班的待机成员，已对全组执行节省停机。\n恢复需人工干预：调高阈值或等待流量包重置。", reason), "breaker")
	} else if cfg["notify_on_breaker"] == "1" && e.store.FlagAge("breaker:"+g.ID+":"+slot) < 0 && !allStopped {
		// 尚未确认全员到位：本轮不通知，下轮 allStopped 后再发（flag 未置）
	}
}

// handleGroupLimitStop 组当班超限且组未启用 failover：只停当班自己，不动待机。
func (e *Engine) handleGroupLimitStop(ctx context.Context, cfg map[string]string, g Group, run *GroupRun,
	dutyInst Instance, st engInstance) {

	slot := run.Slot
	if run.EventID != 0 { // 进行中的换班事件先以 error 收尾
		e.store.EventFinish(run.EventID, "error", "", "当班流量超限且组未启用故障转移")
		run.EventID = 0
	}
	run.Phase = PhaseBreaker
	run.Note = "当班流量超限且无法换班"
	_ = e.store.GroupSave(g)

	if isStoppedAnyMode(st.Status, st.StoppedMode) {
		// 已停到位：清节流标记（同班次重新开机再超限还能再记），报告一次
		e.store.FlagClear("limit_stop_cmd:" + g.ID + ":" + slot)
		e.store.FlagClear("limit_stop_err:" + g.ID + ":" + slot)
		e.store.FlagClear("limit_stop_evt:" + g.ID + ":" + slot)
		if e.store.FlagAge("limit_stop:"+g.ID+":"+slot) < 0 {
			e.store.FlagSet("limit_stop:" + g.ID + ":" + slot)
			e.store.AddEvent(Event{Kind: EvLimitStop, Status: "error",
				Title: fmt.Sprintf("%s：当班 %s 流量超限已停机", groupName(g), dutyInst.Name)})
			e.notifyIf(cfg, "notify_on_limit_stop",
				fmt.Sprintf("%s：当班流量超限已停机", groupName(g)),
				fmt.Sprintf("当班机 %s 已达流量阈值且组未启用故障转移，已自动停机；待机成员未受影响。",
					dutyInst.Name), "limit_stop")
		}
		return
	}
	cmdKey := "limit_stop_cmd:" + g.ID + ":" + slot
	if age := e.store.FlagAge(cmdKey); age >= 0 && age < breakerStopCooldown {
		return
	}
	e.store.FlagSet(cmdKey)
	if e.store.FlagAge("limit_stop_evt:"+g.ID+":"+slot) < 0 {
		e.store.FlagSet("limit_stop_evt:" + g.ID + ":" + slot)
		e.store.AddEvent(Event{Kind: EvLimitStop, Status: "error",
			Title: fmt.Sprintf("%s：当班 %s 流量超限，自动停机", groupName(g), dutyInst.Name)})
	}
	if _, err := e.cloud.StopInstance(ctx, &dutyInst, alicloud.StoppedModeStopCharging, true); err != nil {
		if e.store.FlagAge("limit_stop_err:"+g.ID+":"+slot) < 0 {
			e.store.FlagSet("limit_stop_err:" + g.ID + ":" + slot)
			e.logErr("组超限停机失败 %s: %v", dutyInst.Name, err)
		}
	}
}

// --- 正常推进 --------------------------------------------------------------------

// ensureSwitch 正常换班推进：先开当班 → 等 Running/IP → 停待机 → DDNS → 结算。
func (e *Engine) ensureSwitch(ctx context.Context, cfg map[string]string, g Group, run *GroupRun,
	duty string, states map[string]engInstance, dutyInst Instance, standbyIDs []string, label string) {

	ddnsOn := cfg["ddns_enabled"] == "1"

	// 1) 开当班
	st := states[duty]
	if st.Status != "Running" && !transientStatus[st.Status] {
		// 已结算过当班又不跑了 = 抢占恢复，另立恢复事件（不计换班超时）
		if run.Phase == PhaseSettled || run.Phase == PhaseFailover || run.Phase == PhaseManual {
			if !run.Recovered {
				run.Recovered = true
				run.StartedAt = time.Now()
				_ = e.store.GroupSave(g)
				e.store.AddEvent(Event{Kind: EvRecovery, Status: "running",
					ServerID: duty, PendingStatus: "Running", PendingKind: "keepalive",
					Title:  fmt.Sprintf("%s：当班未运行，已自动拉起", label),
					Detail: "当班机在结算后又不运行（疑被抢占），已重新下发启动指令"})
			}
		} else if run.EventID == 0 {
			run.EventID = e.ensureShiftEvent(g, run)
			_ = e.store.GroupSave(g)
		}
		if err := e.cloud.StartInstance(ctx, &dutyInst); err != nil {
			key := "start_err:" + g.ID + ":" + run.Slot
			if e.store.FlagAge(key) < 0 {
				e.store.FlagSet(key)
				if run.EventID != 0 {
					e.store.EventFinish(run.EventID, "error", "", "启动指令失败："+startFailText(err))
				}
				run.Phase = PhaseError
				run.Note = startFailText(err)
				_ = e.store.GroupSave(g)
			}
			return
		}
		// 指令已发，等下一轮确认
	}

	// 2) 中间态等待 / Running 但无 IP（EIP 绑定延迟）
	if st.Status != "Running" || (ddnsOn && st.PublicIP == "") {
		run.Phase = PhaseSwitching
		run.Stage = StageStartingDuty
		run.Attempts++
		_ = e.store.GroupSave(g)
		e.maybeReportTimeout(cfg, g, run, label)
		return // 不做任何停待机动作
	}

	// 3) 停非当班
	allStopped := true
	for _, sid := range standbyIDs {
		s, ok := states[sid]
		if !ok || !s.live {
			allStopped = false // 读不到：不停它、也不算到位
			continue
		}
		if isStoppedAnyMode(s.Status, s.StoppedMode) {
			continue // 已停到位不重发
		}
		sInst, ok := e.instanceByID(sid)
		if !ok {
			continue
		}
		// 唯一例外：本次 stall failover 被换下的原当班即使还在 Starting 也强制停
		force := run.Failover && run.FailoverReason == "stall" && run.FailoverFrom == sid
		if transientStatus[s.Status] && !force {
			allStopped = false
			continue // 等待落地
		}
		if _, err := e.cloud.StopInstance(ctx, &sInst, alicloud.StoppedModeStopCharging, true); err != nil {
			key := "stop_err:" + sid
			if e.store.FlagAge(key) < 0 {
				e.store.FlagSet(key)
				e.logErr("停止待机失败 %s: %v", sInst.Name, err)
			}
			allStopped = false
		}
	}

	// 4) DDNS（全员停到位后）
	dnsOK := true
	if allStopped && ddnsOn && e.dnsOf != nil {
		statuses := e.dnsOf("group", g.ID)
		statuses = append(statuses, e.dnsOf("server", duty)...)
		combined := combineStatuses(statuses)
		dnsOK = strings.HasPrefix(combined, "✅")
		if !dnsOK && !strings.HasPrefix(combined, "⏸") {
			// 已绑定但失败：等待重试，超时强制放行结算
			key := "ddns_wait:" + g.ID + ":" + run.Slot
			if e.store.FlagAge(key) < 0 {
				e.store.FlagSet(key)
			}
			if e.store.FlagAge(key) < ddnsWaitSeconds {
				run.Phase = PhaseSwitching
				run.Stage = StageStoppingStandby
				_ = e.store.GroupSave(g)
				e.maybeReportTimeout(cfg, g, run, label)
				return
			}
			e.store.AddEvent(Event{Kind: EvError, Status: "error",
				Title: fmt.Sprintf("%s：DDNS 未同步，换班照常结算", label)})
		}
	}

	// 5) 就绪判定与结算
	ready := states[duty].Status == "Running" && allStopped &&
		(states[duty].PublicIP != "" || !ddnsOn) && dnsOK
	if !ready {
		e.maybeReportTimeout(cfg, g, run, label)
		return
	}
	e.settleGroup(ctx, cfg, g, run, duty, label)
}

// settleGroup 首次就绪结算：改题收尾、清临时 flag、发通知。
func (e *Engine) settleGroup(ctx context.Context, cfg map[string]string, g Group, run *GroupRun, duty, label string) {
	settledPhase := PhaseSettled
	switch {
	case run.Failover:
		settledPhase = PhaseFailover
	case run.Manual:
		settledPhase = PhaseManual
	}
	first := !run.Settled
	if first {
		run.Settled = true
		run.Phase = settledPhase
		run.Stage = ""
		_ = e.store.GroupSave(g)

		title := e.settleTitle(g, run, label)
		if run.EventID != 0 {
			e.store.EventFinish(run.EventID, "done", title, "换班完成")
		} else {
			ev := e.store.AddEvent(Event{Kind: EvShiftDone, Status: "done",
				Title: title, Detail: fmt.Sprintf("当班：%s", e.instName(duty))})
			run.EventID = ev
			_ = e.store.GroupSave(g)
		}
		// 清本班次临时 flag
		prefix := g.ID + ":" + run.Slot
		e.store.FlagClear("duty_stall:" + prefix)
		e.store.FlagClear("stall_nocand:" + prefix)
		e.store.FlagClear("stall_disabled:" + prefix)
		e.store.FlagClearPrefix("failover_tried:" + prefix + ":")
		e.store.FlagClear("timeout:" + prefix)

		// 通知（去重键 + 只在需要时发）
		e.notifySettle(cfg, g, run, duty, label)
	} else if run.Phase != settledPhase || run.Stage != "" {
		run.Phase = settledPhase
		run.Stage = ""
		_ = e.store.GroupSave(g)
	}
	// 抢占恢复收尾
	if run.Recovered {
		run.Recovered = false
		run.Phase = settledPhase
		run.Stage = ""
		_ = e.store.GroupSave(g)
		if ev, ok := e.store.PendingEvent(duty, "keepalive"); ok && ev.Kind == EvRecovery {
			e.store.EventFinish(ev.ID, "done", fmt.Sprintf("%s：当班已恢复运行", label), "")
		}
		e.notifyIf(cfg, "notify_on_recovery", label+"：当班已恢复",
			"当班机疑被抢占后已自动拉起并恢复运行。", "recovery")
	}
}

// settleTitle 结算标题（优先级：failover > manual > new_group > 换班完成）。
func (e *Engine) settleTitle(g Group, run *GroupRun, label string) string {
	switch {
	case run.Failover:
		if run.FailoverReason == "stall" {
			return fmt.Sprintf("%s：故障转移完成（保活超时）", label)
		}
		return fmt.Sprintf("%s：故障转移完成（流量超限）", label)
	case run.Manual:
		return fmt.Sprintf("%s：手动换班完成", label)
	case run.Origin == "new_group":
		return fmt.Sprintf("%s：新建换班组", label)
	default:
		return fmt.Sprintf("%s：换班完成", label)
	}
}

// notifySettle 结算通知（带 slot 去重）。
func (e *Engine) notifySettle(cfg map[string]string, g Group, run *GroupRun, duty, label string) {
	key := "notify:" + g.ID + ":" + run.Slot + ":settled"
	kind := "shift"
	if run.Failover {
		key = "notify:" + g.ID + ":" + run.Slot + ":failover"
		if g.Cycle == "month" {
			key += ":" + billDate(time.Now())
		}
		kind = "failover"
	}
	if e.store.FlagAge(key) >= 0 {
		return
	}
	e.store.FlagSet(key)
	var b strings.Builder
	fmt.Fprintf(&b, "换班组 %s 完成切换。\n当班：%s", label, e.instName(duty))
	if run.Failover {
		fmt.Fprintf(&b, "\n原因：原当班 %s %s", e.instName(run.FailoverFrom), failoverReasonText(run.FailoverReason))
	}
	e.notifyIf(cfg, map[bool]string{true: "notify_on_failover", false: "notify_on_shift"}[run.Failover],
		settleTitleShort(g, run, label), b.String(), kind)
}

func settleTitleShort(g Group, run *GroupRun, label string) string {
	if run.Failover {
		return label + "：故障转移"
	}
	return label + "：换班完成"
}

// maybeReportTimeout 换班超时（10 分钟，manual 不报；去重一次）。
func (e *Engine) maybeReportTimeout(cfg map[string]string, g Group, run *GroupRun, label string) {
	if cfg["notify_on_error"] != "1" || run.Manual || run.NotifiedTimeout {
		return
	}
	timeoutMin := settingInt(cfg, "shift_timeout_min", 10)
	if time.Since(run.StartedAt) < time.Duration(timeoutMin)*time.Minute {
		return
	}
	key := "timeout:" + g.ID + ":" + run.Slot
	if e.store.FlagAge(key) >= 0 {
		return
	}
	e.store.FlagSet(key)
	run.NotifiedTimeout = true
	_ = e.store.GroupSave(g)
	msg := fmt.Sprintf("换班超时（超过 %d 分钟仍未就绪），请检查实例状态", timeoutMin)
	e.store.AddEvent(Event{Kind: EvError, Status: "error",
		Title: fmt.Sprintf("%s：换班超时", label)})
	e.notifyIf(cfg, "notify_on_error", label+"：换班超时", msg, "error")
}

// --- 月周期隔天复位 ---------------------------------------------------------------

// resetMonthDayFailover 仅 month 周期：组时区自然日更替时清掉 stall 型代班
// （traffic 型代班顶满整段不复位；manual 不复位）。
func (e *Engine) resetMonthDayFailover(g *Group, t time.Time) {
	run := g.Run
	if run == nil {
		return
	}
	if run.FailoverReason != "stall" || run.Manual || !run.Failover {
		return
	}
	if run.DutyID == "" {
		return
	}
	today := billDate(t)
	if run.DutyID != "" && g.Run != nil && g.Run.Note != "" && strings.Contains(g.Run.Note, today) {
		return // 已按今天处理过
	}
	// 隔日：回归排班表（清覆盖），清本班次 stall 类 flag
	e.store.FlagClearPrefix(g.ID + ":" + run.Slot + ":")
	run.Failover = false
	run.FailoverReason = ""
	run.FailoverFrom = ""
	run.DutyID = ""
	run.Phase = PhaseSwitching
	run.Stage = ""
	run.Note = today
	run.StartedAt = t
	_ = e.store.GroupSave(*g)
}

// --- 手动换班 / 新建组 / 解散 -------------------------------------------------------

// ManualSwitch 手动切换到下一台（hours：锚点平移；month/其他：直接指定）。
func (e *Engine) ManualSwitch(g Group) error {
	t := time.Now()
	members := g.ActiveMembers()
	if len(members) < 2 {
		return errNotFound("有效成员不足 2 台，无法换班")
	}
	duty, _ := e.actualDuty(g, t)
	// 找当前当班在 position 序的下一台
	next := members[0]
	for i, m := range members {
		if m.InstanceID == duty {
			next = members[(i+1)%len(members)]
			break
		}
	}
	run := g.Run
	if run == nil || run.Slot != e.slotFor(g, t) {
		run = &GroupRun{Slot: e.slotFor(g, t), Phase: PhasePending, StartedAt: t}
	}
	run.DutyID = next.InstanceID
	run.Manual = true
	run.Failover = false
	run.Phase = PhaseSwitching
	run.Stage = ""
	run.Settled = false
	run.StartedAt = t
	g.Run = run
	if g.Cycle != "month" && !isTimetableGroup(members) {
		// hours 模式把锚点平移到新当班的班次起点；month/时刻表由 DutyID 覆盖
		g.AnchorAt = shiftAnchorTo(g, members, next, t)
	}
	if err := e.store.GroupSave(g); err != nil {
		return err
	}
	// 旧 running manual/shift 事件收掉，开新事件
	for _, ev := range e.store.RunningEvents() {
		if ev.Kind == EvManual || ev.Kind == EvStart || ev.Kind == EvShiftDone {
			e.store.EventFinish(ev.ID, "done", ev.Title+"（已取消）", "")
		}
	}
	ev := e.store.AddEvent(Event{Kind: EvManual, Status: "running",
		Title:  fmt.Sprintf("%s：手动换班 → %s", groupName(g), e.instName(next.InstanceID)),
		Detail: "等待当班就绪并停掉其余成员"})
	g.Run.EventID = ev
	_ = e.store.GroupSave(g)
	e.RequestFastReconcile()
	return nil
}

// shiftAnchorTo 把 anchor 平移到"下一台刚开始当班"的时刻。
func shiftAnchorTo(g Group, members []GroupMember, next GroupMember, now time.Time) time.Time {
	total := 0
	for _, m := range members {
		total += m.Hours
	}
	if total <= 0 {
		return now
	}
	// 计算从 anchor 到 next 班次起点的偏移：把 next 之前的成员时长从当前周期内扣除
	anchor := g.AnchorAt
	if anchor.IsZero() {
		return now
	}
	elapsed := int(now.Sub(anchor).Seconds())
	cycle := total * 3600
	within := ((elapsed % cycle) + cycle) % cycle
	// 找 next 班次起点在周期内的偏移
	start := 0
	for _, m := range members {
		if m.InstanceID == next.InstanceID {
			break
		}
		start += m.Hours * 3600
	}
	delta := start - within
	if delta > 0 {
		delta -= cycle // 起点在过去：回退一个周期
	}
	return anchor.Add(time.Duration(delta) * time.Second)
}

// BootstrapGroup 新建组引导：按排班对齐组员（不发结算通知）。
func (e *Engine) BootstrapGroup(g Group) {
	g.Run = &GroupRun{
		Slot: e.slotFor(g, time.Now()), Phase: PhaseSwitching,
		Origin: "new_group", StartedAt: time.Now(),
	}
	_ = e.store.GroupSave(g)
	ev := e.store.AddEvent(Event{Kind: EvGroupNew, Status: "running",
		Title:  fmt.Sprintf("%s：新建换班组", g.Name),
		Detail: "按排班表对齐组员状态"})
	g.Run.EventID = ev
	_ = e.store.GroupSave(g)
	e.RequestFastReconcile()
}

// DissolveGroup 解散：不发任何启停指令，组员保持现状；解绑组 DNS、收事件。
func (e *Engine) DissolveGroup(g Group) {
	_ = e.store.UnbindAllFor("group", g.ID)
	for _, ev := range e.store.RunningEvents() {
		if ev.Kind == EvStart || ev.Kind == EvManual || ev.Kind == EvShiftDone || ev.Kind == EvGroupNew {
			e.store.EventFinish(ev.ID, "done", "", "换班组已解散")
		}
	}
	_ = e.store.GroupDelete(g.ID)
}

// --- 辅助 -------------------------------------------------------------------------

// closeOrphanShiftEvents 收掉该组遗留的 running 换班类事件（历史孤儿，
// 如跨班次重建、重启丢 run 后残留的"手动换班/换班进行中"）。
func (e *Engine) closeOrphanShiftEvents(g Group) {
	prefix := groupName(g)
	for _, ev := range e.store.RunningEvents() {
		if ev.Kind != EvStart && ev.Kind != EvManual && ev.Kind != EvShiftDone && ev.Kind != EvGroupNew {
			continue
		}
		if strings.HasPrefix(ev.Title, prefix) {
			e.store.EventFinish(ev.ID, "done", "", "班次状态已由引擎接管")
		}
	}
}

func groupName(g Group) string {
	if g.Name != "" {
		return g.Name
	}
	return "#" + g.ID
}

func (e *Engine) instName(id string) string {
	if in, ok := e.instanceByID(id); ok && in.Name != "" {
		return in.Name
	}
	return id
}

// ensureShiftEvent 复用本班次 running 事件或新建（写 dns_before 快照）。
func (e *Engine) ensureShiftEvent(g Group, run *GroupRun) int64 {
	if run.EventID != 0 {
		return run.EventID
	}
	ev := e.store.AddEvent(Event{
		Kind: EvStart, Status: "running",
		Title: fmt.Sprintf("%s：换班进行中", groupName(g)),
	})
	run.EventID = ev
	_ = e.store.GroupSave(g)
	return ev
}

// combineStatuses 多条记录状态合并：全一致（单条原样/多条注明 N 条），否则逐条拼接。
func combineStatuses(statuses []string) string {
	if len(statuses) == 0 {
		return "⏸ 未绑定"
	}
	var uniq []string
	seen := map[string]bool{}
	for _, s := range statuses {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	if len(uniq) == 1 {
		if len(statuses) == 1 {
			return uniq[0]
		}
		return fmt.Sprintf("%s（%d 条）", uniq[0], len(statuses))
	}
	return strings.Join(uniq, " / ")
}

// settingInt 设置取整（非法/缺省回落 def）。
func settingInt(cfg map[string]string, key string, def int) int {
	v := 0
	for _, c := range cfg[key] {
		if c < '0' || c > '9' {
			return def
		}
		v = v*10 + int(c-'0')
	}
	if v == 0 {
		return def
	}
	return v
}

// billDayOfMonth / billDate：计费口径固定 UTC+8。
func billTime(t time.Time) time.Time {
	return t.In(time.FixedZone("CST", billTZOffset))
}

func billDayOfMonth(t time.Time) int {
	return billTime(t).Day()
}

func billDate(t time.Time) string {
	return billTime(t).Format("2006-01-02")
}
