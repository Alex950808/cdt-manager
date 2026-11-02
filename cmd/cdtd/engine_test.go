package main

// 引擎状态机测试：fake 云驱动保活 / 换班 / 故障转移 / 熔断 / 限额停机。

import (
	"context"
	"testing"
	"time"

	"cdt-manager/alicloud"
)

type fakeState struct {
	status string
	mode   string
	ip     string
}

// fakeCloud 可编程假云：状态机 + 调用记录 + 账号级流量读数。
type fakeCloud struct {
	states      map[string]*fakeState
	traffic     map[string]*float64 // 按实例读数（组员可各用各的 AK）
	starts      []string
	stops       []string
	stopErrFor  map[string]error
	startErrFor map[string]error
}

func (f *fakeCloud) DescribeInstance(ctx context.Context, inst *Instance) (*alicloud.Instance, error) {
	st, ok := f.states[inst.ID]
	if !ok {
		return nil, errNotFound("no such instance")
	}
	return &alicloud.Instance{
		ID: inst.InstanceID, Status: st.status, StoppedMode: st.mode,
		PublicIP: st.ip, RegionID: inst.RegionID,
	}, nil
}

func (f *fakeCloud) StartInstance(ctx context.Context, inst *Instance) error {
	f.starts = append(f.starts, inst.ID)
	if err := f.startErrFor[inst.ID]; err != nil {
		return err
	}
	f.states[inst.ID].status = "Starting"
	return nil
}

func (f *fakeCloud) StopInstance(ctx context.Context, inst *Instance, mode string, fallback bool) (string, error) {
	f.stops = append(f.stops, inst.ID+":"+mode)
	if err := f.stopErrFor[inst.ID]; err != nil {
		return "", err
	}
	f.states[inst.ID].status = "Stopped"
	f.states[inst.ID].mode = mode
	return mode, nil
}

func (f *fakeCloud) InternetTrafficGB(ctx context.Context, inst *Instance) (*float64, error) {
	return f.traffic[inst.ID], nil
}

// newTestEngine 搭建 store + engine（云注入 fake），并预置实例。
func newTestEngine(t *testing.T, fc *fakeCloud, instances ...Instance) (*Store, *Engine) {
	t.Helper()
	store, err := NewStore(t.TempDir()+"/d.json", "")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(store, "http://mock", "")
	eng := NewEngine(store, srv)
	eng.cloud = fc
	for i := range instances {
		in := instances[i]
		if _, err := store.Create(&in); err != nil {
			t.Fatal(err)
		}
	}
	return store, eng
}

func mkInst(id, cloudID, name string) Instance {
	return Instance{ID: id, Name: name, AccessKeyID: "ak", AccessKeySecret: "sk",
		RegionID: "cn-hongkong", InstanceID: cloudID, Site: "china",
		QuotaGB: 200, ThresholdGB: 100}
}

func cfgAllOn() map[string]string {
	return map[string]string{
		"notify_on_shift": "1", "notify_on_failover": "1", "notify_on_recovery": "1",
		"notify_on_breaker": "1", "notify_on_limit_stop": "1", "notify_on_error": "1",
		"notify_on_keepalive": "1", "ddns_enabled": "0", "reconcile_interval": "60",
	}
}

func gb(v float64) *float64 { return &v }

func TestKeepalivePullsStoppedInstance(t *testing.T) {
	fc := &fakeCloud{states: map[string]*fakeState{
		"s1": {status: "Stopped"},
	}}
	store, eng := newTestEngine(t, fc, mkInst("s1", "i-1", "机器一"))
	_ = store.KeepaliveSet("s1", true)

	ctx := context.Background()
	if err := eng.reconcileKeepalive(ctx, cfgAllOn(), Keepalive{InstanceID: "s1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if len(fc.starts) != 1 || fc.starts[0] != "s1" {
		t.Fatalf("starts = %v", fc.starts)
	}
	if _, ok := store.PendingEvent("s1", "keepalive"); !ok {
		t.Error("running keepalive event expected")
	}

	// 拉起完成（Running）→ 计数清零、事件由确认器收尾（这里验证引擎侧不再重发）
	fc.states["s1"].status = "Running"
	if err := eng.reconcileKeepalive(ctx, cfgAllOn(), Keepalive{InstanceID: "s1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if len(fc.starts) != 1 {
		t.Errorf("should not re-start a Running instance, starts=%v", fc.starts)
	}
	if store.FlagValue("keepalive_tries:s1") != 0 {
		t.Errorf("tries should be cleared on Running, got %d", store.FlagValue("keepalive_tries:s1"))
	}
}

func TestKeepaliveOverLimitStopsInsteadOfStart(t *testing.T) {
	fc := &fakeCloud{states: map[string]*fakeState{"s1": {status: "Stopped"}}, traffic: map[string]*float64{"s1": gb(150), "s2": gb(50)}}
	store, eng := newTestEngine(t, fc, mkInst("s1", "i-1", "机器一"))
	_ = store.KeepaliveSet("s1", true)

	ctx := context.Background()
	_ = eng.reconcileKeepalive(ctx, cfgAllOn(), Keepalive{InstanceID: "s1", Enabled: true})
	if len(fc.starts) != 0 {
		t.Errorf("over-limit must not start, starts=%v", fc.starts)
	}
	if len(fc.stops) == 0 {
		t.Fatal("over-limit should stop the instance")
	}
	if store.FlagAge("limit_stop:单机保活:s1") < 0 {
		t.Error("limit_stop flag expected")
	}
	if _, ok := store.PendingEvent("s1", "keepalive"); ok {
		t.Error("keepalive event should not exist (stopped instead of started)")
	}

	// 流量回落 → 标记清除，恢复"停了就拉"
	fc.traffic["s1"] = gb(10)
	_ = eng.reconcileKeepalive(ctx, cfgAllOn(), Keepalive{InstanceID: "s1", Enabled: true})
	if store.FlagAge("limit_stop:单机保活:s1") >= 0 {
		t.Error("limit_stop flag should clear when traffic falls back")
	}
	if len(fc.starts) != 1 {
		t.Errorf("should resume pulling up, starts=%v", fc.starts)
	}
}

func TestGroupShiftSwitchesDuty(t *testing.T) {
	// A 当班（Stopped），B 待机（Running）→ 引擎应开 A、停 B
	fc := &fakeCloud{states: map[string]*fakeState{
		"s1": {status: "Stopped"},
		"s2": {status: "Running", ip: "2.2.2.2"},
	}, traffic: map[string]*float64{"s1": gb(10), "s2": gb(10)}}
	store, eng := newTestEngine(t, fc, mkInst("s1", "i-1", "甲"), mkInst("s2", "i-2", "乙"))
	g := Group{
		ID: "g1", Name: "一组", Enabled: true, FailoverEnabled: true, Cycle: "day",
		AnchorAt: time.Now().Add(-time.Hour), // A 在 0-12h 段 → 当班
		Members: []GroupMember{
			{InstanceID: "s1", Position: 0, Hours: 12},
			{InstanceID: "s2", Position: 1, Hours: 12},
		},
	}
	_ = store.GroupSave(g)

	ctx := context.Background()

	// 第一轮：开 A（A Stopped → start）
	eng.ReconcileAll(ctx)
	if len(fc.starts) != 1 || fc.starts[0] != "s1" {
		t.Fatalf("round1 starts = %v", fc.starts)
	}
	if len(fc.stops) != 0 {
		t.Fatalf("round1 must not stop standby while duty not ready, stops=%v", fc.stops)
	}

	// 第二轮：A Starting（中间态）→ 不停 B
	fc.states["s1"].status = "Starting"
	eng.ReconcileAll(ctx)
	if len(fc.stops) != 0 {
		t.Fatalf("transient duty must hold standby, stops=%v", fc.stops)
	}

	// 第三轮：A Running（无 DDNS → 无需 IP）→ 停 B
	fc.states["s1"].status = "Running"
	eng.ReconcileAll(ctx)
	if len(fc.stops) != 1 || fc.stops[0] != "s2:StopCharging" {
		t.Fatalf("round3 stops = %v", fc.stops)
	}

	// 第四轮：B 已停 → 全员到位 → 结算
	fc.states["s2"].mode = "StopCharging"
	eng.ReconcileAll(ctx)
	ng, _ := store.GroupGet("g1")
	if ng.Run == nil || !ng.Run.Settled {
		t.Fatalf("group should be settled, run=%+v", ng.Run)
	}
	found := false
	for _, ev := range store.EventList(10) {
		if strings_contains(ev.Title, "换班完成") {
			found = true
		}
	}
	if !found {
		t.Error("settle event title expected")
	}
}

func TestGroupTrafficFailover(t *testing.T) {
	// A 当班 Running 且超限（150 > 100），B 健康 → failover：B 接管，A 停
	fc := &fakeCloud{states: map[string]*fakeState{
		"s1": {status: "Running", ip: "1.1.1.1"},
		"s2": {status: "Stopped", mode: "StopCharging"},
	}, traffic: map[string]*float64{"s1": gb(150), "s2": gb(50)}}
	store, eng := newTestEngine(t, fc, mkInst("s1", "i-1", "甲"), mkInst("s2", "i-2", "乙"))
	g := Group{
		ID: "g1", Name: "一组", Enabled: true, FailoverEnabled: true, Cycle: "day",
		AnchorAt: time.Now().Add(-time.Hour),
		Members: []GroupMember{
			{InstanceID: "s1", Position: 0, Hours: 12},
			{InstanceID: "s2", Position: 1, Hours: 12},
		},
	}
	_ = store.GroupSave(g)
	ctx := context.Background()

	// 多轮推进：failover → B 开 → A 停 → 结算
	for i := 0; i < 6; i++ {
		eng.ReconcileAll(ctx)
		if i < 2 {
			fc.states["s2"].status = "Starting" // 模拟 B 启动中
		}
		if i == 2 {
			fc.states["s2"].status = "Running"
			fc.states["s2"].ip = "2.2.2.2"
		}
		if i >= 3 {
			fc.states["s1"].status = "Stopped"
			fc.states["s1"].mode = "StopCharging"
		}
	}
	ng, _ := store.GroupGet("g1")
	if ng.Run == nil || !ng.Run.Failover || ng.Run.FailoverReason != "traffic" {
		t.Fatalf("failover expected, run=%+v", ng.Run)
	}
	if ng.Run.DutyID != "s2" {
		t.Errorf("duty should move to s2, got %q", ng.Run.DutyID)
	}
	if ng.Run.Phase != PhaseFailover {
		t.Errorf("phase = %q, want failover", ng.Run.Phase)
	}
	if !ng.Run.Settled {
		t.Error("failover should settle")
	}
}

func TestGroupBreakerWhenNoCandidate(t *testing.T) {
	// A、B 都超限 → 熔断：全组停机
	fc := &fakeCloud{states: map[string]*fakeState{
		"s1": {status: "Running", ip: "1.1.1.1"},
		"s2": {status: "Stopped", mode: "StopCharging"},
	}, traffic: map[string]*float64{"s1": gb(150), "s2": gb(150)}}
	store, eng := newTestEngine(t, fc, mkInst("s1", "i-1", "甲"), mkInst("s2", "i-2", "乙"))
	g := Group{
		ID: "g1", Name: "一组", Enabled: true, FailoverEnabled: true, Cycle: "day",
		AnchorAt: time.Now().Add(-time.Hour),
		Members: []GroupMember{
			{InstanceID: "s1", Position: 0, Hours: 12},
			{InstanceID: "s2", Position: 1, Hours: 12},
		},
	}
	_ = store.GroupSave(g)
	cfg := cfgAllOn()
	_ = eng.reconcileGroup(context.Background(), cfg, g)

	ng, _ := store.GroupGet("g1")
	if ng.Run == nil || ng.Run.Phase != PhaseBreaker {
		t.Fatalf("breaker phase expected, run=%+v", ng.Run)
	}
	if len(fc.stops) < 1 {
		t.Error("breaker should stop members")
	}
}

func TestManualSwitchMovesDuty(t *testing.T) {
	fc := &fakeCloud{states: map[string]*fakeState{
		"s1": {status: "Running", ip: "1.1.1.1"},
		"s2": {status: "Stopped", mode: "StopCharging"},
	}, traffic: map[string]*float64{"s1": gb(10), "s2": gb(10)}}
	store, eng := newTestEngine(t, fc, mkInst("s1", "i-1", "甲"), mkInst("s2", "i-2", "乙"))
	g := Group{
		ID: "g1", Name: "一组", Enabled: true, FailoverEnabled: true, Cycle: "day",
		AnchorAt: time.Now().Add(-time.Hour),
		Members: []GroupMember{
			{InstanceID: "s1", Position: 0, Hours: 12},
			{InstanceID: "s2", Position: 1, Hours: 12},
		},
	}
	_ = store.GroupSave(g)

	if err := eng.ManualSwitch(g); err != nil {
		t.Fatal(err)
	}
	ng, _ := store.GroupGet("g1")
	if ng.Run == nil || !ng.Run.Manual || ng.Run.DutyID != "s2" {
		t.Fatalf("manual switch to s2 expected, run=%+v", ng.Run)
	}
	duty, _ := eng.actualDuty(ng, time.Now())
	if duty != "s2" {
		t.Errorf("actualDuty = %q, want s2", duty)
	}
}

func strings_contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestSlotForHoursAdvancesAcrossShifts(t *testing.T) {
	// 回归：班次起点必须随时间推进（此前跨入下一班后 slot 不变，
	// 导致换班永不触发、组卡死在"切换中"）
	fc := &fakeCloud{states: map[string]*fakeState{
		"s1": {status: "Running"}, "s2": {status: "Stopped", mode: "StopCharging"},
	}, traffic: map[string]*float64{"s1": gb(10), "s2": gb(10)}}
	store, eng := newTestEngine(t, fc, mkInst("s1", "i-1", "甲"), mkInst("s2", "i-2", "乙"))
	g := Group{
		ID: "g1", Name: "一组", Enabled: true, FailoverEnabled: true, Cycle: "day",
		AnchorAt: time.Date(2025, 10, 31, 15, 26, 0, 0, time.FixedZone("CST", billTZOffset)),
		Members: []GroupMember{
			{InstanceID: "s1", Position: 0, Hours: 12},
			{InstanceID: "s2", Position: 1, Hours: 12},
		},
	}
	_ = store.GroupSave(g)
	tz := time.FixedZone("CST", billTZOffset)
	cases := []struct {
		at     time.Time
		duty   string
		slotPfx string
	}{
		{time.Date(2025, 10, 31, 16, 0, 0, 0, tz), "s1", "2025-10-31T15:26:00+08:00"},
		{time.Date(2025, 11, 1, 3, 41, 0, 0, tz), "s2", "2025-11-01T03:26:00+08:00"}, // 跨入乙班：slot 必须推进
		{time.Date(2025, 11, 1, 15, 30, 0, 0, tz), "s1", "2025-11-01T15:26:00+08:00"},
	}
	for _, c := range cases {
		if got := eng.dutyAt(g, c.at); got != c.duty {
			t.Errorf("dutyAt(%s) = %q, want %q", c.at, got, c.duty)
		}
		if got := eng.slotFor(g, c.at); !hasPrefix(got, c.slotPfx) {
			t.Errorf("slotFor(%s) = %q, want prefix %q", c.at, got, c.slotPfx)
		}
	}
}
