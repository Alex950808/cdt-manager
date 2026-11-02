package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnabledChannels(t *testing.T) {
	cfg := Config{
		"notify_tg_enabled": "1", "notify_tg_token": "t", "notify_tg_chat_id": "1",
		"notify_wh_enabled": "1", "notify_wh_url": "https://x",
		"notify_mail_enabled": "1", "notify_mail_host": "", // 邮件配置不全 → 不启用
	}
	got := EnabledChannels(cfg)
	if len(got) != 2 || got[0] != "telegram" || got[1] != "webhook" {
		t.Errorf("EnabledChannels = %v", got)
	}
}

func TestResultText(t *testing.T) {
	if got := ResultText(map[string]string{"telegram": "ok"}, "换班报告"); got != "换班报告已发送" {
		t.Errorf("ResultText ok = %q", got)
	}
	if got := ResultText(map[string]string{"telegram": "failed: x", "webhook": "ok"}, "报告"); got != "报告发送失败：telegram" {
		t.Errorf("ResultText partial = %q", got)
	}
	if got := ResultText(map[string]string{}, "报告"); got != "" {
		t.Errorf("no channels should be empty, got %q", got)
	}
}

func TestTelegramSendAndFallback(t *testing.T) {
	var bodies []map[string]any
	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// 通过改写 transport 把 api.telegram.org 指到本地
	cfg := Config{"notify_tg_enabled": "1", "notify_tg_token": "tok", "notify_tg_chat_id": "42"}
	msg := Message{Kind: "shift", Title: "标题", HTML: "<b>加粗</b>正文", Text: "正文"}

	t.Run("400 falls back to plain text then success", func(t *testing.T) {
		bodies = nil
		statusSeq := []int{400, 200}
		i := 0
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies = append(bodies, body)
			w.WriteHeader(statusSeq[min(i, len(statusSeq)-1)])
			i++
		})
		cfg["notify_tg_api_base"] = srv.URL // 把 Bot API 指向本地测试服务器
		if err := send(context.Background(), cfg, "telegram", msg); err != nil {
			t.Fatalf("send: %v", err)
		}
		if len(bodies) != 2 {
			t.Fatalf("attempts = %d, want 2", len(bodies))
		}
		if bodies[0]["parse_mode"] != "HTML" || bodies[1]["parse_mode"] != nil {
			t.Errorf("fallback parse_mode wrong: %v then %v", bodies[0]["parse_mode"], bodies[1]["parse_mode"])
		}
		if bodies[1]["text"] != "加粗正文" {
			t.Errorf("plain text fallback = %v", bodies[1]["text"])
		}
	})
}

func TestWebhookTemplateAndDefault(t *testing.T) {
	var gotHeaders http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	msg := Message{Kind: "shift", Title: `香港"主力"换班`, Text: "第一行\n第二行", Fields: nil}

	t.Run("default body", func(t *testing.T) {
		cfg := Config{"notify_wh_enabled": "1", "notify_wh_url": srv.URL}
		if err := send(context.Background(), cfg, "webhook", msg); err != nil {
			t.Fatalf("send: %v", err)
		}
		var parsed map[string]string
		if err := json.Unmarshal(gotBody, &parsed); err != nil {
			t.Fatalf("default body is not valid JSON: %s", gotBody)
		}
		if parsed["title"] != `香港"主力"换班` || parsed["kind"] != "shift" {
			t.Errorf("parsed = %v", parsed)
		}
	})

	t.Run("template with JSON escaping", func(t *testing.T) {
		cfg := Config{
			"notify_wh_enabled": "1", "notify_wh_url": srv.URL,
			"notify_wh_body":    `{"content":"{title}","detail":"{text}"}`,
			"notify_wh_headers": `{"X-Token":"abc"}`,
		}
		if err := send(context.Background(), cfg, "webhook", msg); err != nil {
			t.Fatalf("send: %v", err)
		}
		var parsed map[string]string
		if err := json.Unmarshal(gotBody, &parsed); err != nil {
			t.Fatalf("template body is not valid JSON: %s", gotBody)
		}
		if parsed["content"] != `香港"主力"换班` || parsed["detail"] != "第一行\n第二行" {
			t.Errorf("parsed = %v", parsed)
		}
		if gotHeaders.Get("X-Token") != "abc" {
			t.Errorf("custom header missing")
		}
	})
}

func TestClipTG(t *testing.T) {
	long := strings.Repeat("<b>行</b>\n", 900)
	clipped := clipTG(long)
	if len(clipped) > tgLimit {
		t.Errorf("clip did not bound length: %d", len(clipped))
	}
	if !strings.HasSuffix(clipped, "…（内容过长已截断）") {
		t.Errorf("clip suffix missing")
	}
	if strings.Count(clipped, "<b>") != strings.Count(clipped, "</b>") {
		t.Errorf("clip cut a tag in half")
	}
}
