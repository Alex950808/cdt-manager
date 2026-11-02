package ddns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cdt-manager/cloudflare"
)

func writeJSON(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

func jsonRead(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// fakeCFZone 模拟一个 Cloudflare zone：A 记录现值 currentIP（空=不存在）。
func fakeCFZone(t *testing.T, zone, recordName string, currentIP *string, updates *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/client/v4/zones":
			_ = jsonWrite(w, map[string]any{
				"success": true, "result": []map[string]any{{"id": "z1", "name": zone}}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/dns_records"):
			if *currentIP == "" {
				_ = jsonWrite(w, map[string]any{"success": true, "result": []any{}})
				return
			}
			_ = jsonWrite(w, map[string]any{"success": true, "result": []map[string]any{
				{"id": "r1", "type": "A", "name": recordName, "content": *currentIP, "ttl": 60, "proxied": false}}})
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/dns_records/r1"):
			*updates++
			var body struct {
				Content string `json:"content"`
			}
			_ = jsonRead(r, &body)
			*currentIP = body.Content
			_ = jsonWrite(w, map[string]any{"success": true, "result": map[string]any{"id": "r1"}})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = jsonWrite(w, map[string]any{"success": false, "errors": []map[string]any{{"code": 7003, "message": "no route"}}})
		}
	}))
}

func jsonWrite(w http.ResponseWriter, v any) error {
	return writeJSON(w, v)
}

func TestIsPublicIPv4(t *testing.T) {
	yes := []string{"47.243.1.2", "8.8.8.8", "1.1.1.1"}
	no := []string{"", "abc", "10.0.0.5", "192.168.1.1", "172.16.0.1", "127.0.0.1",
		"169.254.1.1", "0.0.0.0", "224.0.0.1", "::1", "2606:4700::1", "1.2.3.4.5"}
	for _, s := range yes {
		if !IsPublicIPv4(s) {
			t.Errorf("IsPublicIPv4(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsPublicIPv4(s) {
			t.Errorf("IsPublicIPv4(%q) = true, want false", s)
		}
	}
}

func TestRootDomain(t *testing.T) {
	cases := map[string]string{
		"ddns.example.com": "example.com",
		"a.b.example.com":  "example.com",
		"example.com":      "example.com",
		"":                 "",
	}
	for in, want := range cases {
		if got := RootDomain(in); got != want {
			t.Errorf("RootDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSyncFlow(t *testing.T) {
	var updates int
	ip := "1.1.1.1"
	ts := fakeCFZone(t, "example.com", "ddns.example.com", &ip, &updates)
	defer ts.Close()

	s := &Syncer{CF: cloudflare.New("tok", cloudflare.WithBaseURL(ts.URL))}
	tc := Target{Enabled: true, Record: "ddns.example.com"}
	ctx := context.Background()

	// 未启用 → Disabled，不发请求
	if r := s.Sync(ctx, Target{Record: "ddns.example.com"}, "2.2.2.2", false); r.Status != StatusDisabled {
		t.Errorf("disabled = %q", r.Status)
	}
	// 无效 IP（私网 / IPv6 / 空）
	for _, bad := range []string{"192.168.1.5", "::1", ""} {
		if r := s.Sync(ctx, tc, bad, false); r.Status != StatusInvalidIP {
			t.Errorf("invalid ip %q → %q", bad, r.Status)
		}
	}
	// 首次同步 → 更新
	if r := s.Sync(ctx, tc, "2.2.2.2", false); r.Status != StatusSynced || !r.Changed {
		t.Fatalf("first sync = %+v", r)
	}
	if updates != 1 {
		t.Fatalf("updates = %d", updates)
	}
	// 同 IP + 上次成功 → 零请求短路
	tc.LastIP, tc.LastStatus = "2.2.2.2", StatusSynced
	if r := s.Sync(ctx, tc, "2.2.2.2", false); r.Changed || updates != 1 {
		t.Errorf("short-circuit failed: %+v updates=%d", r, updates)
	}
	// 同 IP 但 force → 仍执行
	if r := s.Sync(ctx, tc, "2.2.2.2", true); !r.Changed || updates != 2 {
		t.Errorf("force should still update: %+v updates=%d", r, updates)
	}
	// 记录不存在 → 3 次重试后 NoRecordID
	tc2 := Target{Enabled: true, Record: "ghost.example.com"}
	if r := s.Sync(ctx, tc2, "3.3.3.3", false); r.Status != StatusNoRecordID {
		t.Errorf("missing record = %q (%s)", r.Status, r.Detail)
	}
}

func TestSyncDisabledClient(t *testing.T) {
	s := &Syncer{} // CF 未配置
	r := s.Sync(context.Background(), Target{Enabled: true, Record: "a.b.c"}, "1.2.3.4", false)
	if r.Status != StatusDisabled {
		t.Errorf("nil client = %q", r.Status)
	}
}
