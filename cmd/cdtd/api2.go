package main

// 扩展 API：保活 / 换班组 / DNS / 设置 / 手动对账。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// --- 保活 ---------------------------------------------------------------------

func (s *Server) handleKeepaliveList(w http.ResponseWriter, r *http.Request) {
	type view struct {
		InstanceID string `json:"instance_id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Tries      int    `json:"tries"`
	}
	out := make([]view, 0, 8)
	for _, k := range s.store.KeepaliveList() {
		v := view{InstanceID: k.InstanceID, Status: "未知", Tries: s.store.FlagValue("keepalive_tries:" + k.InstanceID)}
		if inst, ok := s.engine.instanceByID(k.InstanceID); ok {
			v.Name = inst.Name
			if snap, ok := s.store.Snapshot(k.InstanceID); ok && snap.Status != "" {
				v.Status = snap.Status
			}
		}
		out = append(out, v)
	}
	jsonOK(w, out)
}

func (s *Server) handleKeepaliveAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	inst, ok := s.engine.instanceByID(body.InstanceID)
	if !ok {
		jsonErr(w, http.StatusNotFound, "实例不存在")
		return
	}
	if err := s.store.KeepaliveSet(body.InstanceID, true); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.FlagClear("keepalive_conflict:" + body.InstanceID)
	s.logEvent("info", "已启用保活：%s（停了就自动拉起）", inst.Name)
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleKeepaliveDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := id
	if inst, ok := s.engine.instanceByID(id); ok {
		name = inst.Name
	}
	if err := s.store.KeepaliveSet(id, false); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logEvent("info", "已停用保活：%s", name)
	jsonOK(w, map[string]bool{"ok": true})
}

// --- 换班组 ---------------------------------------------------------------------

// groupView 是组对外的完整视图。
type groupView struct {
	Group
	MembersView []map[string]any `json:"members_view"`
	DutyID      string           `json:"duty_id"`
	Phase       string           `json:"phase"`
	DutyLabel   string           `json:"duty_label"`
	NextSwitch  string           `json:"next_switch"`
}

func (s *Server) groupView(g Group) groupView {
	v := groupView{Group: g}
	t := time.Now()
	duty, has := s.engine.actualDuty(g, t)
	if has {
		v.DutyID = duty
	}
	if g.Run != nil {
		v.Phase = g.Run.Phase
	}
	for _, m := range g.ActiveMembers() {
		mv := map[string]any{
			"instance_id": m.InstanceID, "position": m.Position,
			"hours": m.Hours, "days": m.Days,
		}
		if inst, ok := s.engine.instanceByID(m.InstanceID); ok {
			mv["name"] = inst.Name
			if snap, ok := s.store.Snapshot(inst.ID); ok {
				mv["status"] = snap.Status
				if snap.TrafficGB != nil {
					mv["traffic_gb"] = *snap.TrafficGB
				}
			}
			if m.InstanceID == duty {
				mv["duty"] = true
				mv["duty_hours"] = m.Hours
				mv["duty_days"] = m.Days
				mv["duty_start"] = m.StartTime
			}
		}
		v.MembersView = append(v.MembersView, mv)
	}
	v.NextSwitch = e_nextSwitch(s.engine, g, t, duty)
	return v
}

// nextSwitchAt 估算下一次换班时刻（概览页倒计时用）：
// hours = 本班次起点 + 当班时长；month = 本段末日；timetable = 段结束时刻。
func e_nextSwitch(e *Engine, g Group, t time.Time, duty string) string {
	members := g.ActiveMembers()
	var cur *GroupMember
	for i := range members {
		if members[i].InstanceID == duty {
			cur = &members[i]
			break
		}
	}
	if cur == nil {
		return ""
	}
	if g.Cycle == "month" {
		spans := monthLayoutDays(members)
		covered := 0
		for i, m := range members {
			span := spans[i]
			if span <= 0 {
				span = 1
			}
			covered += span
			if m.InstanceID == duty {
				end := time.Date(billTime(t).Year(), billTime(t).Month(), covered+1, 0, 0, 0, 0,
					time.FixedZone("CST", billTZOffset))
				return end.Format(time.RFC3339)
			}
		}
		return ""
	}
	if isTimetableGroup(members) {
		s, _ := parseHHMM(cur.StartTime)
		en, _ := parseHHMM(cur.EndTime)
		if en == 0 {
			en = 1440
		}
		nowMin := billTime(t).Hour()*60 + billTime(t).Minute()
		day := billTime(t)
		if s >= en && nowMin < en {
			day = day.AddDate(0, 0, -1) // 跨天段：起点在昨天
		}
		return day.Add(time.Duration(en) * time.Minute).Format(time.RFC3339)
	}
	// hours：slot 起点 + 当班时长
	run := g.Run
	slotStart := t
	if run != nil {
		if st, err := time.Parse(time.RFC3339, run.Slot); err == nil {
			slotStart = st
		}
	}
	return slotStart.Add(time.Duration(cur.Hours) * time.Hour).Format(time.RFC3339)
}

func (s *Server) handleGroupList(w http.ResponseWriter, r *http.Request) {
	out := make([]groupView, 0, 8)
	for _, g := range s.store.GroupList() {
		out = append(out, s.groupView(g))
	}
	jsonOK(w, out)
}

type groupInput struct {
	Name            string `json:"name"`
	Enabled         *bool  `json:"enabled"`
	FailoverEnabled *bool  `json:"failover_enabled"`
	Cycle           string `json:"cycle"`
	Members         []struct {
		InstanceID string `json:"instance_id"`
		Hours      int    `json:"hours"`
		Days       int    `json:"days"`
		Position   int    `json:"position"`
		StartTime  string `json:"start_time"`
		EndTime    string `json:"end_time"`
	} `json:"members"`
}

// validateGroup 保存校验：≥2 成员、无重复、不跨启用组冲突、
// hours/days 范围。
func (s *Server) validateGroup(in groupInput, selfID string) error {
	if len(in.Members) < 2 {
		return errNotFound("换班组至少需要 2 台成员")
	}
	seen := map[string]bool{}
	anyTime := false
	allTime := true
	for _, m := range in.Members {
		if seen[m.InstanceID] {
			return errNotFound("同一台实例不能在组内出现两次")
		}
		seen[m.InstanceID] = true
		if _, ok := s.engine.instanceByID(m.InstanceID); !ok {
			return errNotFound("成员实例不存在: " + m.InstanceID)
		}
		if in.Cycle == "month" {
			if m.Days < 0 || m.Days > 31 {
				return errNotFound("月周期天数需在 0–31 之间")
			}
		} else if m.StartTime != "" || m.EndTime != "" {
			anyTime = true
			// day 周期：任一成员填了时刻 → 时刻表模式，全员必须成对合法
			if !hasValidTimeWindow(GroupMember{StartTime: m.StartTime, EndTime: m.EndTime}) {
				return errNotFound("时刻表需成对填写合法 HH:MM（结束 00:00 表达到午夜）")
			}
		} else {
			allTime = false
			if m.Hours <= 0 || m.Hours > 168 {
				return errNotFound("hours 需在 1–168 之间（或填写成对的开始/结束时刻）")
			}
		}
	}
	if in.Cycle != "month" && anyTime && !allTime {
		return errNotFound("时刻表排班需全部成员均填写开始/结束时刻")
	}
	// 跨启用组冲突
	for _, g := range s.store.GroupList() {
		if !g.Enabled || g.ID == selfID {
			continue
		}
		for _, m := range in.Members {
			for _, em := range g.ActiveMembers() {
				if em.InstanceID == m.InstanceID {
					return errNotFound("实例已在启用的换班组「" + g.Name + "」中")
				}
			}
		}
	}
	return nil
}

func (s *Server) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	var in groupInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if in.Cycle == "" {
		in.Cycle = "day"
	}
	if in.Enabled == nil {
		en := true
		in.Enabled = &en
	}
	if in.FailoverEnabled == nil {
		en := true
		in.FailoverEnabled = &en
	}
	if err := s.validateGroup(in, ""); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	g := Group{
		ID: newUUID(), Name: in.Name, Enabled: *in.Enabled,
		FailoverEnabled: *in.FailoverEnabled, Cycle: in.Cycle,
		AnchorAt: time.Now(),
	}
	for i, m := range in.Members {
		pos := m.Position
		if pos == 0 {
			pos = i
		}
		g.Members = append(g.Members, GroupMember{
			InstanceID: m.InstanceID, Position: pos, Hours: m.Hours, Days: m.Days,
			StartTime: m.StartTime, EndTime: m.EndTime,
		})
	}
	if err := s.store.GroupSave(g); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logEvent("info", "新建换班组：%s（%d 台成员）", groupName(g), len(g.Members))
	s.engine.BootstrapGroup(g) // 引导对齐（事件 origin=new_group，不发结算通知）
	jsonOK(w, s.groupView(g))
}

func (s *Server) handleGroupUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, ok := s.store.GroupGet(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "换班组不存在")
		return
	}
	var in groupInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if in.Cycle == "" {
		in.Cycle = g.Cycle
	}
	if err := s.validateGroup(in, id); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 成员变更 → 轮换锚点重置
	g.Members = g.Members[:0]
	for i, m := range in.Members {
		pos := m.Position
		if pos == 0 {
			pos = i
		}
		g.Members = append(g.Members, GroupMember{
			InstanceID: m.InstanceID, Position: pos, Hours: m.Hours, Days: m.Days,
			StartTime: m.StartTime, EndTime: m.EndTime,
		})
	}
	g.Name = in.Name
	g.Cycle = in.Cycle
	if in.Enabled != nil {
		g.Enabled = *in.Enabled
	}
	if in.FailoverEnabled != nil {
		g.FailoverEnabled = *in.FailoverEnabled
	}
	g.AnchorAt = time.Now()
	if err := s.store.GroupSave(g); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logEvent("info", "更新换班组：%s", groupName(g))
	s.engine.BootstrapGroup(g)
	jsonOK(w, s.groupView(g))
}

func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, ok := s.store.GroupGet(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "换班组不存在")
		return
	}
	s.engine.DissolveGroup(g) // 不发启停指令；解绑 DNS、收事件、删组
	s.logEvent("info", "解散换班组：%s（组员保持当前状态）", groupName(g))
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleGroupSwitch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, ok := s.store.GroupGet(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "换班组不存在")
		return
	}
	if !g.Enabled {
		jsonErr(w, http.StatusBadRequest, "组已停用")
		return
	}
	if err := s.engine.ManualSwitch(g); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// --- DNS -----------------------------------------------------------------------

func (s *Server) handleDNSList(w http.ResponseWriter, r *http.Request) {
	_ = s.dnsOps.SyncRegistry()
	type view struct {
		DNSRecord
		TargetName   string `json:"target_name"`
		TargetStatus string `json:"target_status"`
		DutyLabel    string `json:"duty_label"`
		State        string `json:"state"`
	}
	out := make([]view, 0, 8)
	for _, rec := range s.store.DNSRecords() {
		v := view{DNSRecord: rec}
		if rec.TargetKind == "" {
			v.State = "unbound"
		} else {
			status, ip, running, duty := s.dnsOps.targetInfo(rec.TargetKind, rec.TargetID)
			v.TargetName = duty
			v.TargetStatus = status
			// 状态机判定（对齐 dns.py state_of）：unbound → pending/idle → error → synced → pending
			switch {
			case !running && !transientStatus[status] && ip == "" && status != "Running":
				if rec.LastStatus == "" {
					v.State = "unbound"
				} else if strings.HasPrefix(rec.LastStatus, "❌") || strings.HasPrefix(rec.LastStatus, "⚠️") {
					v.State = "error"
				} else {
					v.State = "idle"
				}
			case strings.HasPrefix(rec.LastStatus, "❌") || strings.HasPrefix(rec.LastStatus, "⚠️"):
				v.State = "error"
			case rec.LastIP == ip && strings.HasPrefix(rec.LastStatus, "✅"):
				v.State = "synced"
			default:
				v.State = "pending"
			}
		}
		out = append(out, v)
	}
	jsonOK(w, out)
}

func (s *Server) handleDNSRecordsSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Records []string `json:"records"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	clean := make([]string, 0, len(body.Records))
	seen := map[string]bool{}
	for _, n := range body.Records {
		n = strings.TrimSpace(n)
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		clean = append(clean, n)
	}
	raw, _ := json.Marshal(clean)
	if err := s.store.SetSettings(map[string]string{"ddns_records": string(raw)}); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.dnsOps.SyncRegistry(); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logEvent("info", "DNS 记录登记已更新（%d 条）", len(clean))
	jsonOK(w, map[string]bool{"ok": true})
}

// handleDNSRecordDelete 从设置真源移除记录名（sync_registry 会同步清掉登记与绑定）。
func (s *Server) handleDNSRecordDelete(w http.ResponseWriter, r *http.Request) {
	record := r.PathValue("record")
	cfg := s.store.Settings()
	var names []string
	_ = json.Unmarshal([]byte(cfg["ddns_records"]), &names)
	kept := make([]string, 0, len(names))
	found := false
	for _, n := range names {
		if strings.EqualFold(strings.TrimSpace(n), record) {
			found = true
			continue
		}
		kept = append(kept, n)
	}
	if !found {
		jsonErr(w, http.StatusNotFound, "记录不在登记列表里: "+record)
		return
	}
	raw, _ := json.Marshal(kept)
	if err := s.store.SetSettings(map[string]string{"ddns_records": string(raw)}); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.dnsOps.SyncRegistry(); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logEvent("info", "DNS 记录已删除：%s", record)
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleDNSBind(w http.ResponseWriter, r *http.Request) {
	record := r.PathValue("record")
	var body struct {
		Kind     string `json:"kind"`
		TargetID string `json:"target_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if body.Kind != "" && body.Kind != "server" && body.Kind != "group" {
		jsonErr(w, http.StatusBadRequest, "kind 只能是 server / group / 空（解绑）")
		return
	}
	if body.Kind != "" {
		if _, ok := s.engine.instanceByID(body.TargetID); body.Kind == "server" && !ok {
			jsonErr(w, http.StatusNotFound, "绑定目标实例不存在")
			return
		}
		if _, ok := s.store.GroupGet(body.TargetID); body.Kind == "group" && !ok {
			jsonErr(w, http.StatusNotFound, "绑定目标换班组不存在")
			return
		}
	}
	if err := s.store.BindDNSRecord(record, body.Kind, body.TargetID); err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.logEvent("info", "DNS 绑定已更新：%s → %s", record, orDash(body.Kind))
	// 绑定即时同步（启用时）
	if body.Kind != "" && s.store.Settings()["ddns_enabled"] == "1" {
		statuses := s.dnsOps.SyncTarget(body.Kind, body.TargetID, "", false)
		s.logEvent("info", "%s：%s", record, combineStatuses(statuses))
	}
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleDNSRecordSync(w http.ResponseWriter, r *http.Request) {
	record := r.PathValue("record")
	rec, ok := s.store.GetDNSRecord(record)
	if !ok {
		jsonErr(w, http.StatusNotFound, "记录未登记")
		return
	}
	statuses := s.dnsOps.SyncTarget(rec.TargetKind, rec.TargetID, "", true) // force 忽略冷却
	jsonOK(w, map[string]string{"status": combineStatuses(statuses)})
}

func (s *Server) handleDNSSyncAll(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Settings()
	if cfg["ddns_enabled"] != "1" {
		jsonErr(w, http.StatusBadRequest, "DDNS 未启用（请先在设置里开启并配置 Cloudflare Token）")
		return
	}
	s.dnsOps.Reconcile(cfg) // force 场景：逐条走 sync（冷却由 Reconcile 内部处理）
	jsonOK(w, map[string]bool{"ok": true})
}

// --- 设置 -----------------------------------------------------------------------

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Settings()
	// 密钥类打码
	for _, k := range []string{"notify_tg_token", "notify_mail_password", "notify_wh_headers", "ddns_cf_key", "ddns_cf_token"} {
		if v := cfg[k]; len(v) > 8 {
			cfg[k] = v[:4] + "****"
		} else if len(v) > 0 {
			cfg[k] = "****"
		}
	}
	jsonOK(w, cfg)
}

func (s *Server) handleSettingsSet(w http.ResponseWriter, r *http.Request) {
	var patch map[string]string
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	// 打码值（含 ****）不覆盖真实值
	real := s.store.Settings()
	for _, k := range []string{"notify_tg_token", "notify_mail_password", "notify_wh_headers", "ddns_cf_key", "ddns_cf_token"} {
		if v, ok := patch[k]; ok && strings.Contains(v, "****") {
			patch[k] = real[k]
		}
	}
	// 间隔钳制
	if v, ok := patch["reconcile_interval"]; ok {
		patch["reconcile_interval"] = clampStr(v, 30, 3600)
	}
	if v, ok := patch["billing_interval"]; ok {
		patch["billing_interval"] = clampStr(v, 60, 86400)
	}
	if err := s.store.SetSettings(patch); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logEvent("info", "设置已保存")
	jsonOK(w, map[string]bool{"ok": true})
}

func clampStr(v string, min, max int) string {
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return v // 非数字原样交由 settingInt 兜底
		}
		n = n*10 + int(c-'0')
	}
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// handleNotifyTest 通知渠道连通性测试。
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	channel := r.PathValue("channel")
	cfg := s.store.Settings()
	if channel == "ddns" {
		status, detail := s.dnsOps.TestDDNS()
		jsonOK(w, map[string]string{"status": status, "detail": detail})
		return
	}
	msg := buildMessage("test", "测试通知",
		"这是一条来自 CDT 管理台的测试通知。收到即说明渠道配置正确。")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results := notifySendAll(ctx, cfg, msg)
	if state, ok := results[channel]; ok {
		if state == "ok" {
			jsonOK(w, map[string]string{"status": "✅ 已发送", "detail": channel})
			return
		}
		jsonErr(w, http.StatusBadGateway, state)
		return
	}
	jsonErr(w, http.StatusBadRequest, "渠道未启用或配置不全："+channel)
}

// handleSamples 返回实例的流量采样序列（曲线用）。
func (s *Server) handleSamples(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	jsonOK(w, s.store.SamplesOf(id))
}

// handleReconcile 手动触发一轮对账（异步执行，立即返回）。
func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		s.engine.ReconcileAll(ctx)
		s.dnsOps.Reconcile(s.store.Settings())
	}()
	jsonOK(w, map[string]bool{"ok": true})
}
