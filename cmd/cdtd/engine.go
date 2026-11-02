package main

// 引擎核心：保活对账 + 闲散机器超限对账。
// 具备完整幂等性：同一状态反复对账不重复动作/不重复推送，
// 幂等载体 = 持久化 flags。

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cdt-manager/alicloud"
)

// Engine 是保活/换班引擎。ReconcileAll 非阻塞抢锁：上一轮没跑完跳过本轮。
type Engine struct {
	store *Store
	cloud CloudAPI

	mu       sync.Mutex
	fastWave time.Time // 快速对账窗口截止时刻

	dnsOf func(kind, targetID string) []string // DDNS 同步回调（组/机器 → 各记录状态文案）
}

func NewEngine(store *Store, srv *Server) *Engine {
	return &Engine{store: store, cloud: srv}
}

// CloudAPI 是引擎对云操作的抽象（Server 实现；测试注入 fake）。
// 方法都带实例配置：不同实例可用不同凭证。
type CloudAPI interface {
	DescribeInstance(ctx context.Context, inst *Instance) (*alicloud.Instance, error)
	StartInstance(ctx context.Context, inst *Instance) error
	StopInstance(ctx context.Context, inst *Instance, stoppedMode string, withFallback bool) (string, error)
	InternetTrafficGB(ctx context.Context, inst *Instance) (*float64, error)
}

// Server 实现 CloudAPI：转发到对应实例的云客户端。
func (s *Server) DescribeInstance(ctx context.Context, inst *Instance) (*alicloud.Instance, error) {
	return s.clientFor(inst).DescribeInstance(ctx, inst.RegionID, inst.InstanceID)
}

func (s *Server) StartInstance(ctx context.Context, inst *Instance) error {
	return s.clientFor(inst).StartInstance(ctx, inst.RegionID, inst.InstanceID)
}

func (s *Server) StopInstance(ctx context.Context, inst *Instance, stoppedMode string, withFallback bool) (string, error) {
	return s.clientFor(inst).StopInstance(ctx, inst.RegionID, inst.InstanceID, stoppedMode, withFallback)
}

func (s *Server) InternetTrafficGB(ctx context.Context, inst *Instance) (*float64, error) {
	return s.clientFor(inst).InternetTrafficGB(ctx)
}

// RequestFastReconcile 申请快速对账窗口（10s 间隔持续 90s）。
func (e *Engine) RequestFastReconcile() {
	e.mu.Lock()
	e.fastWave = time.Now().Add(fastWindow)
	e.mu.Unlock()
}

// fastActive 是否处于快速窗口。
func (e *Engine) fastActive() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return time.Now().Before(e.fastWave)
}

// ReconcileAll 一轮对账：保活 → 闲散超限 → 换班组。子流程异常只记事件不中断。
func (e *Engine) ReconcileAll(ctx context.Context) {
	if !e.mu.TryLock() {
		return // 上一轮没跑完，跳过本轮（不排队）
	}
	defer e.mu.Unlock()

	cfg := e.store.Settings()
	for _, k := range e.store.KeepaliveList() {
		if err := e.reconcileKeepalive(ctx, cfg, k); err != nil {
			e.logErr("keepalive 对账异常 %s: %v", k.InstanceID, err)
		}
	}
	e.reconcileIdleLimits(ctx, cfg)
	for _, g := range e.store.GroupList() {
		if err := e.reconcileGroup(ctx, cfg, g); err != nil {
			e.logErr("换班组对账异常 %s: %v", g.Name, err)
		}
	}
}

// logErr 引擎错误 → 事件流（error 级）。
func (e *Engine) logErr(format string, args ...any) {
	e.store.AddEvent(Event{Kind: EvError, Title: fmt.Sprintf(format, args...), Status: "error"})
}

// engInstance 是引擎眼中的一台机器：配置 + 实时状态 + 流量。
type engInstance struct {
	Instance
	Status      string
	StoppedMode string
	PublicIP    string
	TrafficGB   *float64 // nil = 本轮未取得读数
	live        bool     // 本轮是否成功读到状态
}

// refresh 实时拉取单机状态与流量（_refresh 语义）。
func (e *Engine) refresh(ctx context.Context, cfg map[string]string, inst Instance) engInstance {
	out := engInstance{Instance: inst, Status: "Unknown"}
	// 状态
	if info, err := e.cloud.DescribeInstance(ctx, &inst); err == nil {
		out.Status, out.StoppedMode = info.Status, info.StoppedMode
		out.PublicIP = info.PublicIP
		out.live = true
		// Running 但瞬时缺 IP：保留旧 IP 不清空
		if info.Status == "Running" && info.PublicIP == "" {
			if snap, ok := e.store.Snapshot(inst.ID); ok {
				out.PublicIP = snap.PublicIP
			}
		}
	} else {
		return out // 状态读不到时不追流量
	}
	// 流量（账号级）
	if gb, err := e.cloud.InternetTrafficGB(ctx, &inst); err == nil {
		out.TrafficGB = gb
	} else {
		e.store.FlagSet("traffic_query_err:" + inst.ID)
	}
	// 落样本（空读数不落，防曲线插假 0；5 分钟限频）
	if out.TrafficGB != nil {
		e.store.AddSample(inst.ID, *out.TrafficGB, time.Now())
	}
	// 落快照（引擎实时数据同步给仪表盘）
	if snap, ok := e.store.Snapshot(inst.ID); ok {
		snap.Status, snap.StoppedMode, snap.PublicIP, snap.RegionID =
			out.Status, out.StoppedMode, out.PublicIP, inst.RegionID
		snap.TrafficGB = out.TrafficGB
		snap.RefreshedAt = time.Now()
		e.store.PutSnapshot(inst.ID, &snap)
	} else {
		e.store.PutSnapshot(inst.ID, &Snapshot{
			Status: out.Status, StoppedMode: out.StoppedMode, PublicIP: out.PublicIP,
			RegionID: inst.RegionID, TrafficGB: out.TrafficGB, RefreshedAt: time.Now(),
		})
	}
	return out
}

// groupedServerIDs 被"启用的组（启用成员≥2）"接管的机器集合——
// 这些机器归组引擎管，保活让位、闲散检查跳过。
func (e *Engine) groupedServerIDs() map[string]bool {
	taken := map[string]bool{}
	for _, g := range e.store.GroupList() {
		if !g.Enabled || len(g.ActiveMembers()) < 2 {
			continue
		}
		for _, m := range g.ActiveMembers() {
			taken[m.InstanceID] = true
		}
	}
	return taken
}

// instanceByID 取实例配置。
func (e *Engine) instanceByID(id string) (Instance, bool) {
	for _, in := range e.store.List() {
		if in.ID == id {
			return *in, true
		}
	}
	return Instance{}, false
}

// --- 保活 ---------------------------------------------------------------------

// reconcileKeepalive 单机保活对账："停了就拉起来"，被组接管时让位。
func (e *Engine) reconcileKeepalive(ctx context.Context, cfg map[string]string, k Keepalive) error {
	if !k.Enabled {
		return nil
	}
	inst, ok := e.instanceByID(k.InstanceID)
	if !ok {
		// 机器已删：事件收尾 + 清计数
		if ev, ok := e.store.PendingEvent(k.InstanceID, "keepalive"); ok {
			e.store.EventFinish(ev.ID, "error", "", "目标实例已删除，停止保活")
		}
		e.store.FlagClear("keepalive_tries:" + k.InstanceID)
		return nil
	}
	// 让位规则：被启用的组接管 → 不动作；第一次发现发一次性告警
	if e.groupedServerIDs()[k.InstanceID] {
		if e.store.FlagAge("keepalive_conflict:"+k.InstanceID) < 0 {
			e.store.FlagSet("keepalive_conflict:" + k.InstanceID)
			e.store.AddEvent(Event{Kind: EvWarning, Title: fmt.Sprintf("%s：已加入换班组，保活让位",
				inst.Name), Status: "done"})
		}
		return nil
	}
	// 状态读取失败：去重告警一次，恢复后自动清
	s := e.refresh(ctx, cfg, inst)
	if !s.live {
		if e.store.FlagAge("keepalive_err:"+k.InstanceID) < 0 {
			e.store.FlagSet("keepalive_err:" + k.InstanceID)
			e.store.AddEvent(Event{Kind: EvError, Status: "error",
				Title: fmt.Sprintf("%s：保活状态查询失败", inst.Name)})
		}
		return nil
	}
	e.store.FlagClear("keepalive_err:" + k.InstanceID)

	// 先判流量：超限走限额停机，不再拉起
	if overThreshold(s.TrafficGB, inst.Threshold()) {
		e.limitStopServer(ctx, cfg, inst, "单机保活", s.TrafficGB)
		return nil
	}
	e.store.FlagClear("limit_stop:单机保活:" + k.InstanceID)

	switch s.Status {
	case "Running":
		// 到位：清计数（确认器也会清，这里双保险；只认 Running，中间态不动）
		e.store.FlagClear("keepalive_tries:" + k.InstanceID)
	case "Stopped":
		// 中断计数：同一条 running 事件复用，进度只在里程碑追加
		triesKey := "keepalive_tries:" + k.InstanceID
		tries := 1
		if ev, ok := e.store.PendingEvent(k.InstanceID, "keepalive"); ok {
			tries = e.store.FlagBump(triesKey)
			e.store.EventUpdate(ev.ID, fmt.Sprintf("%s：保活拉起中", inst.Name))
			if keepaliveMilestones[tries] {
				e.store.EventAppendProgress(ev.ID,
					fmt.Sprintf("已尝试拉起 %d 次，仍未运行（%s）", tries, s.Status))
			}
		} else {
			e.store.FlagClear(triesKey)
			e.store.FlagBump(triesKey)
			e.store.AddEvent(Event{
				Kind: EvStart, Status: "running", ServerID: k.InstanceID,
				PendingStatus: "Running", PendingKind: "keepalive",
				Title:  fmt.Sprintf("%s：疑被抢占，自动拉起", inst.Name),
				Detail: "保活目标处于 Stopped，已下发启动指令",
			})
		}
		if err := e.cloud.StartInstance(ctx, &inst); err != nil {
			// 启动指令失败（如库存不足）：去重告警一次；无重试上限，一直试
			if e.store.FlagAge("keepalive_fail:"+k.InstanceID) < 0 {
				e.store.FlagSet("keepalive_fail:" + k.InstanceID)
				e.store.AddEvent(Event{Kind: EvError, Status: "error",
					Title: fmt.Sprintf("%s：保活启动失败：%s", inst.Name, startFailText(err))})
			}
		} else {
			e.store.FlagClear("keepalive_fail:" + k.InstanceID)
		}
	default:
		// Starting/Pending/Stopping：正在启动的实例不重复开机
	}
	return nil
}

// limitStopServer 单机限额停止（作用域：单机保活 / 闲散手动）：
// 超限就停它自己，事件与通知各去重一次；流量回落后由调用方清标记恢复。
func (e *Engine) limitStopServer(ctx context.Context, cfg map[string]string, inst Instance, scope string, used *float64) {
	flagKey := "limit_stop:" + scope + ":" + inst.ID
	first := e.store.FlagAge(flagKey) < 0
	e.store.FlagSet(flagKey)
	if first {
		e.store.AddEvent(Event{Kind: EvLimitStop, Status: "error",
			Title: fmt.Sprintf("%s：流量已达阈值，自动停机（%s）",
				inst.Name, trafficUsedText(used))})
		e.notifyIf(cfg, "notify_on_limit_stop", fmt.Sprintf("%s：流量超限已停机", inst.Name),
			fmt.Sprintf("实例 %s（%s）当月流量已达阈值，已自动节省停机。恢复保活/开机需流量回落或调高阈值。",
				inst.Name, inst.InstanceID), "limit_stop")
	}
	// 停机指令 120s 节流：状态读不到的机器不会一直重发
	cmdKey := "limit_stop_cmd:" + scope + ":" + inst.ID
	if age := e.store.FlagAge(cmdKey); age >= 0 && age < breakerStopCooldown {
		return
	}
	e.store.FlagSet(cmdKey)
	if _, err := e.cloud.StopInstance(ctx, &inst, alicloud.StoppedModeStopCharging, true); err != nil {
		if e.store.FlagAge("limit_stop_err:"+scope+":"+inst.ID) < 0 {
			e.store.FlagSet("limit_stop_err:" + scope + ":" + inst.ID)
			e.logErr("超限停机失败 %s: %v", inst.Name, err)
		}
	}
}

// --- 闲散机器超限 ---------------------------------------------------------------

// reconcileIdleLimits 不属于任何启用组、也不挂保活的机器：超限即停（scope=闲散手动）。
func (e *Engine) reconcileIdleLimits(ctx context.Context, cfg map[string]string) {
	taken := e.groupedServerIDs()
	for _, inst := range e.store.List() {
		if taken[inst.ID] || e.store.KeepaliveHas(inst.ID) {
			continue
		}
		s := e.refresh(ctx, cfg, *inst)
		if !s.live {
			continue
		}
		flagKey := "limit_stop:闲散手动:" + inst.ID
		if overThreshold(s.TrafficGB, inst.Threshold()) {
			e.limitStopServer(ctx, cfg, *inst, "闲散手动", s.TrafficGB)
		} else {
			e.store.FlagClear(flagKey)
		}
	}
}

// --- 公共小件 -------------------------------------------------------------------

// notifyIf 开关开启时发通知（开关只决定发不发，不决定记不记）。
func (e *Engine) notifyIf(cfg map[string]string, switchKey, title, text, kind string) {
	if cfg[switchKey] != "1" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg := buildMessage(kind, title, text)
		results := notifySendAll(ctx, cfg, msg)
		if line := notifyResultText(results, "通知"); line != "" {
			e.store.AddEvent(Event{Kind: kind, Title: line, Status: "done"})
		}
	}()
}

// trafficUsedText 把流量读数翻成文案。
func trafficUsedText(used *float64) string {
	if used == nil {
		return "流量读数不可用"
	}
	return fmt.Sprintf("已用 %.2f GB", *used)
}
