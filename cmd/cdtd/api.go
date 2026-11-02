package main

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"cdt-manager/alicloud"
)

// Server 是管理后台：REST API + 内嵌 Web UI。
type Server struct {
	store       *Store
	baseURL     string // 透传 alicloud.WithBaseURL（mock / 网关）
	authToken   string // API 鉴权口令（空 = 不启用鉴权）
	login       loginGuard
	mu          sync.Mutex
	clients     map[string]*alicloud.Client // 按实例 ID 缓存的云客户端（带余额缓存）
	engine      *Engine                     // 保活/换班引擎
	dnsOps      *DNSOps
	schedCancel context.CancelFunc
}

func NewServer(store *Store, baseURL string, authToken string) *Server {
	s := &Server{
		store:     store,
		baseURL:   baseURL,
		authToken: authToken,
		clients:   map[string]*alicloud.Client{},
	}
	s.engine = NewEngine(store, s)
	s.dnsOps = NewDNSOps(store, s.engine)
	s.engine.dnsOf = func(kind, targetID string) []string {
		return s.dnsOps.SyncTarget(kind, targetID, "", false)
	}
	return s
}

// clientFor 取（或创建）实例对应的云客户端。实例凭证变更时调用方应 dropClient。
func (s *Server) clientFor(inst *Instance) *alicloud.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[inst.ID]; ok {
		return c
	}
	opts := []alicloud.Option{alicloud.WithBalanceCache(6 * time.Hour)}
	if s.baseURL != "" {
		opts = append(opts, alicloud.WithBaseURL(s.baseURL))
	}
	c := alicloud.New(inst.AccessKeyID, inst.AccessKeySecret, opts...)
	s.clients[inst.ID] = c
	return c
}

func (s *Server) dropClient(id string) {
	s.mu.Lock()
	delete(s.clients, id)
	s.mu.Unlock()
}

// --- HTTP 装配 ---------------------------------------------------------------

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/instances", s.handleList)
	mux.HandleFunc("POST /api/instances", s.handleCreate)
	mux.HandleFunc("PUT /api/instances/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/instances/{id}", s.handleDelete)
	mux.HandleFunc("POST /api/instances/{id}/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/instances/{id}/start", s.handleStart)
	mux.HandleFunc("POST /api/instances/{id}/stop", s.handleStop)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("DELETE /api/events", s.handleClearEvents)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/setup/status", s.handleInitStatus)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("GET /api/auth/me", s.handleMe)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/auth/password", s.handleChangePassword)

	// 保活
	mux.HandleFunc("GET /api/keepalives", s.handleKeepaliveList)
	mux.HandleFunc("POST /api/keepalives", s.handleKeepaliveAdd)
	mux.HandleFunc("DELETE /api/keepalives/{id}", s.handleKeepaliveDelete)
	// 换班组
	mux.HandleFunc("GET /api/groups", s.handleGroupList)
	mux.HandleFunc("POST /api/groups", s.handleGroupCreate)
	mux.HandleFunc("PUT /api/groups/{id}", s.handleGroupUpdate)
	mux.HandleFunc("DELETE /api/groups/{id}", s.handleGroupDelete)
	mux.HandleFunc("POST /api/groups/{id}/switch", s.handleGroupSwitch)
	// DNS
	mux.HandleFunc("GET /api/dns", s.handleDNSList)
	mux.HandleFunc("POST /api/dns/records", s.handleDNSRecordsSave)
	mux.HandleFunc("DELETE /api/dns/records/{record}", s.handleDNSRecordDelete)
	mux.HandleFunc("POST /api/dns/records/{record}/bind", s.handleDNSBind)
	mux.HandleFunc("POST /api/dns/records/{record}/sync", s.handleDNSRecordSync)
	mux.HandleFunc("POST /api/dns/sync", s.handleDNSSyncAll)
	// 设置 / 对账
	mux.HandleFunc("GET /api/settings", s.handleSettingsGet)
	mux.HandleFunc("POST /api/settings", s.handleSettingsSet)
	mux.HandleFunc("POST /api/settings/test/{channel}", s.handleNotifyTest)
	mux.HandleFunc("POST /api/reconcile", s.handleReconcile)
	mux.HandleFunc("GET /api/instances/{id}/samples", s.handleSamples)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /assets/{path...}", s.handleAsset)
	mux.HandleFunc("GET /apple-touch-icon.png", s.handlePublicFile("apple-touch-icon.png"))
	mux.HandleFunc("GET /manifest.webmanifest", s.handlePublicFile("manifest.webmanifest"))
	mux.HandleFunc("GET /icon-192.png", s.handlePublicFile("icon-192.png"))
	mux.HandleFunc("GET /icon-512.png", s.handlePublicFile("icon-512.png"))
	return mux
}

// jsonErr 统一错误响应体。
func jsonErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonOK(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(body)
}

// --- 实例 CRUD ---------------------------------------------------------------

// instanceView 是对外的实例视图：密钥打码。
type instanceView struct {
	Instance
	HasSecret    bool      `json:"has_secret"`
	SecretMasked string    `json:"secret_masked"`
	Snapshot     *Snapshot `json:"snapshot,omitempty"`
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

func (s *Server) view(inst *Instance) instanceView {
	// 密钥绝不外发：视图里清空原值，只留打码形态
	safe := *inst
	safe.AccessKeySecret = ""
	v := instanceView{Instance: safe, HasSecret: inst.AccessKeySecret != "", SecretMasked: maskSecret(inst.AccessKeySecret)}
	if snap, ok := s.store.Snapshot(inst.ID); ok {
		v.Snapshot = &snap
	}
	return v
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	list := s.store.List()
	views := make([]instanceView, 0, len(list))
	for _, inst := range list {
		views = append(views, s.view(inst))
	}
	jsonOK(w, views)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in Instance
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	if err := validateInstance(&in, true); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Site = normalizeSite(in.Site)
	id, err := s.store.Create(&in)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logEvent("info", "新增实例 %s（%s @ %s）", in.Name, in.InstanceID, in.RegionID)
	created, _ := s.store.Get(id)
	jsonOK(w, s.view(&created))
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in Instance
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	in.ID = id
	if err := validateInstance(&in, false); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Site = normalizeSite(in.Site)
	if err := s.store.Update(&in); err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.dropClient(id) // 凭证可能变了，重建客户端
	s.logEvent("info", "更新实例配置 %s", in.Name)
	updated, _ := s.store.Get(id)
	jsonOK(w, s.view(&updated))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := ""
	if inst, ok := s.store.Get(id); ok {
		name = inst.Name
	}
	if err := s.store.Delete(id); err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	_ = s.store.KeepaliveSet(id, false)
	_ = s.store.UnbindAllFor("server", id)
	s.dropClient(id)
	s.logEvent("info", "删除实例 %s", orDash(name))
	jsonOK(w, map[string]bool{"ok": true})
}

func validateInstance(in *Instance, creating bool) error {
	if creating && (in.AccessKeyID == "" || in.AccessKeySecret == "") {
		return errors.New("AccessKey ID / Secret 不能为空")
	}
	// 更新模式：空字段 = 保持原值（store.Update 语义），不做非空校验
	if creating && (in.InstanceID == "" || in.RegionID == "") {
		return errors.New("实例 ID 与地域不能为空")
	}
	return nil
}

func normalizeSite(s string) string {
	if strings.EqualFold(s, "international") {
		return string(alicloud.SiteInternational)
	}
	return string(alicloud.SiteChina)
}

// --- 云操作 ------------------------------------------------------------------

// refreshOne 拉取单实例全部云数据并落快照（状态 / 流量 / 余额 / 账单并行）。
func (s *Server) refreshOne(inst *Instance) *Snapshot {
	c := s.clientFor(inst)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snap := &Snapshot{RefreshedAt: time.Now()}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	setErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	wg.Add(4)
	go func() { // 实例状态
		defer wg.Done()
		inst2, err := c.DescribeInstance(ctx, inst.RegionID, inst.InstanceID)
		if err != nil {
			setErr(err)
			return
		}
		mu.Lock()
		snap.Status, snap.StoppedMode = inst2.Status, inst2.StoppedMode
		snap.PublicIP, snap.RegionID = inst2.PublicIP, inst2.RegionID
		mu.Unlock()
	}()
	go func() { // 当月流量（账号维度）
		defer wg.Done()
		gb, err := c.InternetTrafficGB(ctx)
		if err != nil {
			setErr(err)
			return
		}
		mu.Lock()
		snap.TrafficGB = gb
		mu.Unlock()
	}()
	go func() { // 余额
		defer wg.Done()
		b, err := c.AccountBalance(ctx, alicloud.Site(inst.Site))
		if err != nil {
			s.logEvent("warn", "查询 %s 可用余额失败: %v", inst.Name, err)
			setErr(err)
			return
		}
		mu.Lock()
		snap.Balance, snap.Currency = b.AvailableAmount, b.Currency
		mu.Unlock()
	}()
	go func() { // 账单
		defer wg.Done()
		bill, err := c.InstanceBill(ctx, alicloud.Site(inst.Site), currentCycle(), inst.InstanceID)
		if err != nil {
			setErr(err)
			return
		}
		mu.Lock()
		snap.BillTotal, snap.BillCycle = bill.Total, bill.Cycle
		mu.Unlock()
	}()
	wg.Wait()

	if firstErr != nil {
		if snap.Status == "" {
			snap.RefreshError = firstErr.Error()
		} else {
			snap.RefreshError = "部分数据刷新异常: " + firstErr.Error()
		}
	}
	s.store.PutSnapshot(inst.ID, snap)
	return snap
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		jsonErr(w, http.StatusNotFound, "实例不存在")
		return
	}
	snap := s.refreshOne(&inst)
	if snap.RefreshError != "" {
		s.logEvent("error", "刷新 %s 失败: %s", inst.Name, snap.RefreshError)
	} else {
		s.logEvent("info", "刷新 %s：状态 %s，流量 %s，余额 %.2f %s，账单 %.2f",
			inst.Name, statusText(snap.Status), trafficText(snap), snap.Balance, orDash(snap.Currency), snap.BillTotal)
	}
	jsonOK(w, s.view(&inst))
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		jsonErr(w, http.StatusNotFound, "实例不存在")
		return
	}
	c := s.clientFor(&inst)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.StartInstance(ctx, inst.RegionID, inst.InstanceID); err != nil {
		msg := startFailText(err)
		s.logEvent("error", "启动 %s 失败: %s", inst.Name, msg)
		jsonErr(w, http.StatusBadGateway, msg)
		return
	}
	s.store.AddEvent(Event{
		Kind:          EvStart,
		Status:        "running",
		ServerID:      inst.ID,
		PendingStatus: "Running",
		PendingKind:   "manual",
		Title:         fmt.Sprintf("已提交启动：%s（%s）", inst.Name, inst.InstanceID),
	})
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		jsonErr(w, http.StatusNotFound, "实例不存在")
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // 空请求体 = 默认节省停机
	mode := alicloud.StoppedModeStopCharging
	if body.Mode == alicloud.StoppedModeStop {
		mode = alicloud.StoppedModeStop
	}
	c := s.clientFor(&inst)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	effective, err := c.StopInstance(ctx, inst.RegionID, inst.InstanceID, mode, true)
	if err != nil {
		s.logEvent("error", "停止 %s 失败: %v", inst.Name, err)
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	title := fmt.Sprintf("已提交节省停机：%s", inst.Name)
	kind := "info"
	if effective != alicloud.StoppedModeStopCharging {
		if mode == alicloud.StoppedModeStopCharging {
			title = fmt.Sprintf("已提交普通停机：%s（不支持节省停机，仍按规格计费）", inst.Name)
			kind = "warn"
		} else {
			title = fmt.Sprintf("已提交普通停机：%s", inst.Name)
			kind = "info"
		}
	}
	s.store.AddEvent(Event{
		Kind:          kind,
		Status:        "running",
		ServerID:      inst.ID,
		PendingStatus: "Stopped",
		PendingKind:   "manual",
		Title:         title,
	})
	jsonOK(w, map[string]string{"effective_mode": effective})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, s.store.EventList(200))
}

func (s *Server) handleClearEvents(w http.ResponseWriter, r *http.Request) {
	s.store.EventClearAll()
	s.logEvent("info", "事件日志已清空")
	jsonOK(w, map[string]bool{"ok": true})
}

// currentCycle 当前账期（YYYY-MM）。
func currentCycle() string { return time.Now().Format("2006-01") }

// logEvent 把操作日志写进持久化事件流（level: info / warn / error）。
func (s *Server) logEvent(level, format string, args ...any) {
	status := "done"
	if level == "error" {
		status = "error"
	}
	s.store.AddEvent(Event{Kind: level, Title: fmt.Sprintf(format, args...), Status: status})
}

// --- 登录 --------------------------------------------------------------------

// loginGuard 是登录口令的防爆破守卫：连续失败达阈值后锁定一段时间。
type loginGuard struct {
	mu          sync.Mutex
	fails       int
	lockedUntil time.Time
}

const (
	loginMaxFails    = 10
	loginLockSeconds = 60
)

func (g *loginGuard) allow() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Now().After(g.lockedUntil)
}

func (g *loginGuard) fail() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fails++
	if g.fails >= loginMaxFails {
		g.lockedUntil = time.Now().Add(loginLockSeconds * time.Second)
		g.fails = 0
	}
}

func (g *loginGuard) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fails = 0
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func trafficText(snap *Snapshot) string {
	if snap.TrafficGB == nil {
		return "无明细"
	}
	return fmt.Sprintf("%.2fGB", *snap.TrafficGB)
}

// startFailText 把启动失败翻译成可读文案（库存不足最常见）。
func startFailText(err error) string {
	var apiErr *alicloud.APIError
	if errors.As(err, &apiErr) && apiErr.IsInsufficientInventory() {
		return "库存不足（InsufficientInventory），该地域暂无可用抢占式库存"
	}
	return err.Error()
}
