package main

import (
	"sort"
	"time"
)

// --- 存储扩展：换班组 / 保活 / 事件 / 标记 ------------------------------------
//
// flags 是引擎幂等性的载体：同一班次反复对账不重复操作不重复推送，
// 重启后不丢（set 不覆盖、bump 累加、age 取已过时长）。

type flagEntry struct {
	CreatedAt time.Time `json:"created_at"`
	Value     int       `json:"value"`
}

// GroupList / GroupGet / GroupSave / GroupDelete ----------
func (s *Store) GroupList() []Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Group, len(s.data.Groups))
	copy(out, s.data.Groups)
	return out
}

func (s *Store) GroupGet(id string) (Group, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, g := range s.data.Groups {
		if g.ID == id {
			return g, true
		}
	}
	return Group{}, false
}

func (s *Store) GroupSave(g Group) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ex := range s.data.Groups {
		if ex.ID == g.ID {
			s.data.Groups[i] = g
			return s.save()
		}
	}
	s.data.Groups = append(s.data.Groups, g)
	return s.save()
}

func (s *Store) GroupDelete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.data.Groups[:0]
	found := false
	for _, g := range s.data.Groups {
		if g.ID == id {
			found = true
			continue
		}
		kept = append(kept, g)
	}
	if !found {
		return errNotFound("换班组不存在")
	}
	s.data.Groups = kept
	return s.save()
}

// KeepaliveSet 启用/停用某实例的保活（ UNIQUE per instance）。
func (s *Store) KeepaliveSet(instanceID string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, k := range s.data.Keepalives {
		if k.InstanceID == instanceID {
			if enabled {
				s.data.Keepalives[i].Enabled = true
			} else {
				s.data.Keepalives = append(s.data.Keepalives[:i], s.data.Keepalives[i+1:]...)
			}
			return s.save()
		}
	}
	if !enabled {
		return nil
	}
	s.data.Keepalives = append(s.data.Keepalives, Keepalive{InstanceID: instanceID, Enabled: true})
	return s.save()
}

func (s *Store) KeepaliveList() []Keepalive {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Keepalive, len(s.data.Keepalives))
	copy(out, s.data.Keepalives)
	return out
}

func (s *Store) KeepaliveHas(instanceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.data.Keepalives {
		if k.InstanceID == instanceID && k.Enabled {
			return true
		}
	}
	return false
}

// --- 事件（持久化，替代内存环形） -------------------------------------------

// AddEvent 新建事件，返回 ID。非 running 事件的 detail 按行拆成进度。
func (s *Store) AddEvent(e Event) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.EventSeq++
	e.ID = s.data.EventSeq
	e.At = time.Now()
	e.UpdatedAt = e.At
	if e.Status == "" {
		e.Status = "done"
	}
	if e.Status != "running" && e.Detail != "" {
		for _, line := range splitLines(e.Detail) {
			e.Progress = appendProgress(e.Progress, line)
		}
		e.Detail = ""
	}
	s.data.Events = append(s.data.Events, e)
	if len(s.data.Events) > 2000 { // 硬上限防膨胀（定时清理之外的最后防线）
		s.data.Events = s.data.Events[len(s.data.Events)-1500:]
	}
	_ = s.save()
	return e.ID
}

// EventFinish 收尾并追加一行进度。title 非空时改题。
func (s *Store) EventFinish(id int64, status, title, progress string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.data.Events) - 1; i >= 0; i-- {
		if s.data.Events[i].ID != id {
			continue
		}
		e := &s.data.Events[i]
		if title != "" {
			e.Title = title
		}
		e.Status = status
		e.UpdatedAt = time.Now()
		if progress != "" {
			e.Progress = appendProgress(e.Progress, progress)
		}
		if status != "running" {
			e.PendingStatus = ""
			e.PendingKind = ""
		}
		_ = s.save()
		return
	}
}

// EventAppendProgress 追加进度（相邻去重 + 封顶丢最旧）。
func (s *Store) EventAppendProgress(id int64, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.data.Events) - 1; i >= 0; i-- {
		if s.data.Events[i].ID != id {
			continue
		}
		s.data.Events[i].Progress = appendProgress(s.data.Events[i].Progress, line)
		s.data.Events[i].UpdatedAt = time.Now()
		_ = s.save()
		return
	}
}

// EventUpdate 原地改题/详情（running 事件复用）。
func (s *Store) EventUpdate(id int64, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.data.Events) - 1; i >= 0; i-- {
		if s.data.Events[i].ID != id {
			continue
		}
		if title != "" {
			s.data.Events[i].Title = title
		}
		s.data.Events[i].UpdatedAt = time.Now()
		_ = s.save()
		return
	}
}

// PendingEvent 找某实例某 kind 的 running 事件（保活等场景复用同一条）。
func (s *Store) PendingEvent(serverID, kind string) (Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.data.Events) - 1; i >= 0; i-- {
		e := s.data.Events[i]
		if e.Status == "running" && e.ServerID == serverID && (kind == "" || e.PendingKind == kind || e.Kind == kind) {
			return e, true
		}
	}
	return Event{}, false
}

// RunningEvents 全部 running 事件（确认器扫描用）。
func (s *Store) RunningEvents() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Event
	for _, e := range s.data.Events {
		if e.Status == "running" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// EventList 按时间倒序取最近 n 条。
func (s *Store) EventList(n int) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.data.Events
	if n > len(all) {
		n = len(all)
	}
	out := make([]Event, n)
	for i := 0; i < n; i++ {
		out[i] = all[len(all)-1-i]
	}
	return out
}

// HasRunningEventKind 是否存在指定 kind 的 running 事件（_shift_busy 判定用）。
func (s *Store) HasRunningEventKind(kinds ...string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.data.Events {
		if e.Status != "running" {
			continue
		}
		for _, k := range kinds {
			if e.Kind == k {
				return true
			}
		}
	}
	return false
}

// --- 流量采样 -------------------------------------------------------------------

const sampleMinInterval = 5 * time.Minute // 同实例采样节流（JSON 存储体积考虑）

// AddSample 落一条流量采样（限频：距上次不足 5 分钟则跳过；空读数由调用方过滤）。
func (s *Store) AddSample(instanceID string, gb float64, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Samples == nil {
		s.data.Samples = map[string][]Sample{}
	}
	cur := s.data.Samples[instanceID]
	if n := len(cur); n > 0 && at.Sub(cur[n-1].At) < sampleMinInterval {
		return
	}
	s.data.Samples[instanceID] = append(cur, Sample{At: at, GB: gb})
	if len(s.data.Samples[instanceID]) > 2200 { // 7 天上限的兜底
		s.data.Samples[instanceID] = s.data.Samples[instanceID][len(s.data.Samples[instanceID])-2000:]
	}
	_ = s.save()
}

// SamplesOf 返回实例的采样序列（时间升序）。
func (s *Store) SamplesOf(instanceID string) []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Sample, len(s.data.Samples[instanceID]))
	copy(out, s.data.Samples[instanceID])
	return out
}

// EventClearAll 清空全部事件（界面「清空」按钮）。
func (s *Store) EventClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Events = nil
	s.data.EventSeq = 0
	_ = s.save()
}

// PruneOld 按时间清理：事件 14 天、flags 90 天、采样 7 天。
func (s *Store) PruneOld(eventsBefore, flagsBefore time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	samplesBefore := time.Now().Add(-7 * 24 * time.Hour)
	kept := s.data.Events[:0]
	for _, e := range s.data.Events {
		if e.At.Before(eventsBefore) {
			continue
		}
		kept = append(kept, e)
	}
	eventsChanged := len(kept) != len(s.data.Events)
	if eventsChanged {
		s.data.Events = kept
	}
	changed := false
	for k, f := range s.data.Flags {
		if f.CreatedAt.Before(flagsBefore) {
			delete(s.data.Flags, k)
			changed = true
		}
	}
	for id, samples := range s.data.Samples {
		keptS := samples[:0]
		for _, sm := range samples {
			if sm.At.Before(samplesBefore) {
				continue
			}
			keptS = append(keptS, sm)
		}
		if len(keptS) != len(samples) {
			s.data.Samples[id] = keptS
			changed = true
		}
	}
	if changed || eventsChanged {
		_ = s.save()
	}
}

// --- flags -------------------------------------------------------------------

// FlagSet 置标记（已存在不覆盖 created_at —— 幂等去重的核心语义）。
func (s *Store) FlagSet(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Flags == nil {
		s.data.Flags = map[string]flagEntry{}
	}
	if _, ok := s.data.Flags[key]; ok {
		return
	}
	s.data.Flags[key] = flagEntry{CreatedAt: time.Now()}
	_ = s.save()
}

// FlagBump 累加计数标记（保活重试次数），返回新值；首置时 created_at 归零。
func (s *Store) FlagBump(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Flags == nil {
		s.data.Flags = map[string]flagEntry{}
	}
	e := s.data.Flags[key]
	e.Value++
	s.data.Flags[key] = e
	_ = s.save()
	return e.Value
}

// FlagValue 读计数。
func (s *Store) FlagValue(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Flags[key].Value
}

// FlagAge 返回标记已过时长；未设置返回 -1（-1 = 可立即执行）。
func (s *Store) FlagAge(key string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data.Flags[key]
	if !ok {
		return -1
	}
	return time.Since(e.CreatedAt)
}

// FlagClear 删除单个标记。
func (s *Store) FlagClear(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Flags[key]; !ok {
		return
	}
	delete(s.data.Flags, key)
	_ = s.save()
}

// FlagClearPrefix 删除全部前缀匹配标记。
func (s *Store) FlagClearPrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for k := range s.data.Flags {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(s.data.Flags, k)
			changed = true
		}
	}
	if changed {
		_ = s.save()
	}
}

// --- 辅助 ---------------------------------------------------------------------

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// appendProgress 相邻去重 + 封顶丢最旧。
func appendProgress(cur []string, line string) []string {
	if len(cur) > 0 && cur[len(cur)-1] == line {
		return cur
	}
	cur = append(cur, line)
	if len(cur) > eventProgressMax {
		cur = cur[len(cur)-eventProgressMax:]
	}
	return cur
}
