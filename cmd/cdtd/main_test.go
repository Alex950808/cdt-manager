package main

import (
	"encoding/json"
	"fmt"

	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Store -------------------------------------------------------------------

func TestStoreCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := NewStore(path, "")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	id, err := s.Create(&Instance{Name: "A", AccessKeyID: "ak", AccessKeySecret: "sk-9876543210", RegionID: "cn-hongkong", InstanceID: "i-1", Site: "china"})
	if err != nil || id == "" {
		t.Fatalf("Create: %v id=%q", err, id)
	}

	// 编辑时不重填密钥 → 保持原值
	if err := s.Update(&Instance{ID: id, Name: "A2", InstanceID: "i-1", RegionID: "cn-hongkong", Site: "china"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get(id)
	if got.Name != "A2" || got.AccessKeySecret != "sk-9876543210" || got.AccessKeyID != "ak" {
		t.Errorf("update semantics broken: %+v", got)
	}

	// 快照持久化（重新打开文件仍在）
	s.PutSnapshot(id, &Snapshot{Status: "Running", Balance: 9.9, Currency: "CNY", RefreshedAt: time.Now()})
	s2, err := NewStore(path, "")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	snap, ok := s2.Snapshot(id)
	if !ok || snap.Status != "Running" || snap.Balance != 9.9 {
		t.Errorf("snapshot not persisted: %+v ok=%v", snap, ok)
	}

	if err := s2.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s2.Snapshot(id); ok {
		t.Error("snapshot should be removed with instance")
	}
}

func TestStoreCRUD_SQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := NewStore(path, "")
	if err != nil {
		t.Fatalf("NewStore SQLite: %v", err)
	}
	defer s.Close()

	id, err := s.Create(&Instance{Name: "A", AccessKeyID: "ak", AccessKeySecret: "sk-9876543210", RegionID: "cn-hongkong", InstanceID: "i-1", Site: "china"})
	if err != nil || id == "" {
		t.Fatalf("Create: %v id=%q", err, id)
	}

	// 编辑时不重填密钥 → 保持原值
	if err := s.Update(&Instance{ID: id, Name: "A2", InstanceID: "i-1", RegionID: "cn-hongkong", Site: "china"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get(id)
	if got.Name != "A2" || got.AccessKeySecret != "sk-9876543210" || got.AccessKeyID != "ak" {
		t.Errorf("update semantics broken: %+v", got)
	}

	// 快照持久化（重新打开数据库仍在）
	trafficVal := 123.45
	s.PutSnapshot(id, &Snapshot{Status: "Running", Balance: 9.9, Currency: "CNY", TrafficGB: &trafficVal, RefreshedAt: time.Now()})
	_ = s.Close()

	s2, err := NewStore(path, "")
	if err != nil {
		t.Fatalf("reopen SQLite: %v", err)
	}
	defer s2.Close()

	snap, ok := s2.Snapshot(id)
	if !ok || snap.Status != "Running" || snap.Balance != 9.9 || snap.TrafficGB == nil || *snap.TrafficGB != 123.45 {
		t.Errorf("snapshot not persisted in SQLite: %+v ok=%v", snap, ok)
	}

	if err := s2.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s2.Snapshot(id); ok {
		t.Error("snapshot should be removed with instance")
	}
}

func TestStore_MigrateJSONToSQLite(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "cdtd-data.json")
	dbPath := filepath.Join(dir, "cdtd-data.db")

	// 1. 先用 JSON 创建数据
	sj, err := NewStore(jsonPath, "")
	if err != nil {
		t.Fatalf("NewStore JSON: %v", err)
	}
	id, err := sj.Create(&Instance{Name: "ToMigrate", AccessKeyID: "ak-mig", AccessKeySecret: "sk-mig", RegionID: "cn-hongkong", InstanceID: "i-mig", Site: "china"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sj.AdminSet(AdminAccount{Username: "admin_mig", PassHash: "hash123"}); err != nil {
		t.Fatalf("AdminSet: %v", err)
	}
	_ = sj.Close()

	// 2. 用 SQLite 打开 dbPath（同目录自动识别迁移源）
	sdb, err := NewStore(dbPath, "")
	if err != nil {
		t.Fatalf("NewStore SQLite with migration: %v", err)
	}
	defer sdb.Close()

	inst, ok := sdb.Get(id)
	if !ok || inst.Name != "ToMigrate" || inst.AccessKeyID != "ak-mig" {
		t.Fatalf("instance migration failed: %+v ok=%v", inst, ok)
	}
	admin, hasAdmin := sdb.AdminGet()
	if !hasAdmin || admin.Username != "admin_mig" || admin.PassHash != "hash123" {
		t.Fatalf("admin migration failed: %+v has=%v", admin, hasAdmin)
	}
}

func TestMaskSecret(t *testing.T) {
	if got := maskSecret("abcd1234efgh"); got != "****efgh" {
		t.Errorf("maskSecret = %q", got)
	}
	if got := maskSecret("ab"); got != "****" {
		t.Errorf("short mask = %q", got)
	}
}

// --- Server API（连 fake 阿里云全链路） ---------------------------------------

// fakeAliyun 精简版假端点：只按 Action 回数据，忽略签名（签名正确性
// 已在 alicloud 包测试中用金标准与服务端重算双重验证过）。
func fakeAliyun(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("Action")
		if action == "" {
			action = r.Header.Get("X-Acs-Action")
		}
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "DescribeInstances":
			_, _ = w.Write([]byte(`{"RequestId":"r","Instances":{"Instance":[{
				"InstanceId":"i-t1","Status":"Running","RegionId":"cn-hongkong","ZoneId":"cn-hongkong-a",
				"EipAddress":{"IpAddress":"1.2.3.4"},"PublicIpAddress":{"IpAddress":[]}}]}}`))
		case "ListCdtInternetTraffic":
			_, _ = w.Write([]byte(`{"RequestId":"r","TrafficDetails":[{"InstanceId":"i-t1","Traffic":1073741824}]}`))
		case "QueryAccountBalance":
			_, _ = w.Write([]byte(`{"Code":"Success","Data":{"AvailableAmount":"88.50","Currency":"CNY"}}`))
		case "DescribeInstanceBill":
			_, _ = w.Write([]byte(`{"Code":"Success","Data":{"BillingCycle":"2025-10","Items":[{"PretaxAmount":12.34}]}}`))
		default:
			_, _ = w.Write([]byte(`{"RequestId":"r"}`))
		}
	}))
}

func TestServerAPIEndToEnd(t *testing.T) {
	cloud := fakeAliyun(t)
	defer cloud.Close()

	store, _ := NewStore(filepath.Join(t.TempDir(), "data.json"), "")
	srv := NewServer(store, cloud.URL, "") // baseURL → fake 云

	id, err := store.Create(&Instance{Name: "测试机", AccessKeyID: "ak", AccessKeySecret: "sk-9876543210",
		RegionID: "cn-hongkong", InstanceID: "i-t1", Site: "china"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	h := srv.Routes()

	// 1) 列表：密钥打码、无快照
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/instances", nil))
	if rec.Code != 200 {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	var views []instanceView
	mustJSON(t, rec.Body.String(), &views)
	if len(views) != 1 || views[0].SecretMasked != "****3210" || views[0].AccessKeySecret != "" {
		t.Fatalf("secret must be masked in API output: %+v", views[0])
	}
	if views[0].Snapshot != nil {
		t.Fatalf("no snapshot expected yet")
	}

	// 2) 刷新：状态 / 流量 / 余额 / 账单 全部落快照（响应是单个视图）
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/instances/"+id+"/refresh", nil))
	if rec.Code != 200 {
		t.Fatalf("refresh status = %d: %s", rec.Code, rec.Body.String())
	}
	var refreshed instanceView
	mustJSON(t, rec.Body.String(), &refreshed)
	v := refreshed
	if v.Snapshot == nil {
		t.Fatal("snapshot missing after refresh")
	}
	if v.Snapshot.Status != "Running" || v.Snapshot.PublicIP != "1.2.3.4" {
		t.Errorf("status/ip wrong: %+v", v.Snapshot)
	}
	if v.Snapshot.TrafficGB == nil || *v.Snapshot.TrafficGB != 1.0 {
		t.Errorf("traffic GB = %v, want 1.0", v.Snapshot.TrafficGB)
	}
	if v.Snapshot.Balance != 88.5 || v.Snapshot.BillTotal != 12.34 {
		t.Errorf("balance/bill wrong: %+v", v.Snapshot)
	}

	// 3) 启动 / 停止
	for _, path := range []string{"/api/instances/" + id + "/start", "/api/instances/" + id + "/stop"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(`{}`)))
		if rec.Code != 200 {
			t.Errorf("%s status = %d: %s", path, rec.Code, rec.Body.String())
		}
	}

	// 4) 事件流包含操作记录
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/events", nil))
	var evs []Event
	mustJSON(t, rec.Body.String(), &evs)
	if len(evs) < 3 {
		t.Errorf("events = %d, want >=3 (refresh/start/stop)", len(evs))
	}

	// 5) 删除
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/instances/"+id, nil))
	if rec.Code != 200 {
		t.Errorf("delete status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/instances", nil))
	mustJSON(t, rec.Body.String(), &views)
	if len(views) != 0 {
		t.Errorf("list should be empty after delete, got %d", len(views))
	}
}

func TestTokenMiddleware(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "d.json"), "")
	srv := NewServer(store, "", "s3cret") // 静态口令模式

	rec := httptest.NewRecorder()
	srv.withAuth(srv.Routes()).ServeHTTP(rec, httptest.NewRequest("GET", "/api/instances", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token should be 401, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/instances", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	srv.withAuth(srv.Routes()).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("valid token should pass, got %d", rec.Code)
	}
}

func TestLoginEndpoint(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "d.json"), "")
	srv := NewServer(store, "", "s3cret")
	h := srv.withAuth(srv.Routes())

	login := func(token string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		body := fmt.Sprintf(`{"token":%q}`, token)
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/login", strings.NewReader(body)))
		return rec
	}

	// 正确口令
	if rec := login("s3cret"); rec.Code != 200 {
		t.Errorf("correct token = %d: %s", rec.Code, rec.Body.String())
	}
	// 错误口令
	if rec := login("wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token = %d, want 401", rec.Code)
	}
	// 连续错误达阈值 → 锁定，之后连正确口令也 429
	for i := 0; i < loginMaxFails; i++ {
		login("wrong")
	}
	if rec := login("s3cret"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("locked login = %d, want 429", rec.Code)
	}
	// 锁定期内，鉴权中间件本身不受影响（Bearer 直连业务 API 仍可用）
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/instances", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	srv.withAuth(srv.Routes()).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("bearer API should be unaffected by login lockout, got %d", rec.Code)
	}
}

func TestAccountAuthFlow(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "d.json"), "")
	srv := NewServer(store, "", "") // 无管理员无静态口令 → 初始免鉴权
	h := srv.withAuth(srv.Routes())

	// 未初始化：业务 API 免鉴权（首次部署引导期）
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/instances", nil))
	if rec.Code != 200 {
		t.Fatalf("pre-init should be open, got %d", rec.Code)
	}
	// 初始化状态探测
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/setup/status", nil))
	if !strings.Contains(rec.Body.String(), "initialized\":false") {
		t.Fatalf("init status = %s", rec.Body.String())
	}
	// 创建管理员（空密码拒绝）
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/setup",
		strings.NewReader(`{"username":"admin","password":""}`)))
	if rec.Code != 400 {
		t.Errorf("empty password = %d, want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/setup",
		strings.NewReader(`{"username":"admin","password":"long-enough-pass"}`)))
	if rec.Code != 200 {
		t.Fatalf("setup = %d: %s", rec.Code, rec.Body.String())
	}
	// 初始化后：业务 API 必须 401
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/instances", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-init should require auth, got %d", rec.Code)
	}
	// setup 不可重复
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/setup",
		strings.NewReader(`{"username":"x","password":"long-enough-pass"}`)))
	if rec.Code != http.StatusForbidden {
		t.Errorf("re-setup = %d, want 403", rec.Code)
	}
	// 登录（错误密码 / 正确密码）
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/login",
		strings.NewReader(`{"username":"admin","password":"wrong-pass-here"}`)))
	if rec.Code != 401 {
		t.Errorf("wrong password = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/login",
		strings.NewReader(`{"username":"admin","password":"long-enough-pass"}`)))
	if rec.Code != 200 {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body.String())
	}
	var lr struct {
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	mustJSON(t, rec.Body.String(), &lr)
	if lr.Token == "" || lr.Username != "admin" {
		t.Fatalf("login response = %+v", lr)
	}
	// me
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+lr.Token)
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"username":"admin"`) {
		t.Errorf("me = %s", rec.Body.String())
	}
	// 改密 → 旧密码失效 → 新密码可登录
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/auth/password",
		strings.NewReader(`{"old":"long-enough-pass","new":"brand-new-pass-1"}`))
	req.Header.Set("Authorization", "Bearer "+lr.Token)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("change password = %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/login",
		strings.NewReader(`{"username":"admin","password":"brand-new-pass-1"}`)))
	if rec.Code != 200 {
		t.Errorf("new password login = %d", rec.Code)
	}
	// 登出 → 会话吊销
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+lr.Token)
	h.ServeHTTP(rec, req)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+lr.Token)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("after logout me = %d, want 401", rec.Code)
	}
}

func mustJSON(t *testing.T, raw string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		t.Fatalf("bad json %q: %v", raw, err)
	}
}
