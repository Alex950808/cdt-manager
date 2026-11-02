package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeCF 模拟 Cloudflare v4 API 的最小行为（zone 查询 / 记录查询 / 记录更新）。
func fakeCF(t *testing.T, zone, recordName, currentIP string, calls *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":9103,"message":"Unknown X-Auth-Key or User Service Key"}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/client/v4/zones":
			name := r.URL.Query().Get("name")
			if name != zone {
				_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "result": []map[string]any{{"id": "zone-1", "name": zone}},
			})
		case r.Method == "GET" && r.URL.Path == "/client/v4/zones/zone-1/dns_records":
			if currentIP == "" {
				_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result":  []map[string]any{{"id": "rec-1", "type": "A", "name": recordName, "content": currentIP, "ttl": 1, "proxied": false}},
			})
		case r.Method == "PUT" && r.URL.Path == "/client/v4/zones/zone-1/dns_records/rec-1":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			currentIP = body["content"].(string) // 更新后内存生效
			*calls = append(*calls, "PUT content="+currentIP)
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec-1"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":7003,"message":"no route"}]}`))
		}
	}))
}

func TestUpsertAChangesAndSkips(t *testing.T) {
	var calls []string
	ts := fakeCF(t, "example.com", "ddns.example.com", "1.1.1.1", &calls)
	defer ts.Close()

	c := New("test-token", WithBaseURL(ts.URL))
	ctx := context.Background()

	// IP 不同 → 更新，changed=true
	changed, err := c.UpsertA(ctx, "example.com", "ddns.example.com", "2.2.2.2")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	// IP 相同 → 跳过，changed=false
	changed, err = c.UpsertA(ctx, "example.com", "ddns.example.com", "2.2.2.2")
	if err != nil || changed {
		t.Fatalf("no-change path: changed=%v err=%v", changed, err)
	}
	joined := ""
	for _, s := range calls {
		joined += s + " | "
	}
	for _, want := range []string{
		"GET /client/v4/zones?name=example.com",
		"GET /client/v4/zones/zone-1/dns_records?type=A&name=ddns.example.com",
		"PUT content=2.2.2.2",
	} {
		if !contains(joined, want) {
			t.Errorf("calls missing %q in: %s", want, joined)
		}
	}
}

func TestUpsertAErrors(t *testing.T) {
	var calls []string
	ts := fakeCF(t, "example.com", "ddns.example.com", "", &calls) // 无记录
	defer ts.Close()

	c := New("test-token", WithBaseURL(ts.URL))
	if _, err := c.UpsertA(context.Background(), "example.com", "ddns.example.com", "3.3.3.3"); err == nil {
		t.Error("missing record should error (no silent create)")
	}
	if _, err := c.UpsertA(context.Background(), "other.com", "x.other.com", "3.3.3.3"); err == nil {
		t.Error("unknown zone should error")
	}
	// 坏 token
	c2 := New("bad", WithBaseURL(ts.URL))
	if _, err := c2.UpsertA(context.Background(), "example.com", "ddns.example.com", "3.3.3.3"); err == nil {
		t.Error("bad token should error")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
