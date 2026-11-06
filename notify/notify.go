package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Message 是一条待发送通知。HTML 与 Text 是同一内容的两个版本：
// Telegram / 邮件用 HTML，Webhook 默认模板用纯文本。
type Message struct {
	Kind   string // shift / keepalive / failover / breaker / limitstop / daily / error
	Title  string
	HTML   string
	Text   string
	Fields map[string]string
}

// Config 是设置键值对。
type Config map[string]string

func (c Config) get(key string) string { return strings.TrimSpace(c[key]) }

// EnabledChannels 返回配置齐全且开启的渠道列表（顺序即发送顺序）。
func EnabledChannels(cfg Config) []string {
	var out []string
	if cfg.get("notify_tg_enabled") == "1" && cfg.get("notify_tg_token") != "" && cfg.get("notify_tg_chat_id") != "" {
		out = append(out, "telegram")
	}
	if cfg.get("notify_mail_enabled") == "1" && cfg.get("notify_mail_host") != "" && cfg.get("notify_mail_to") != "" {
		out = append(out, "email")
	}
	if cfg.get("notify_wh_enabled") == "1" && cfg.get("notify_wh_url") != "" {
		out = append(out, "webhook")
	}
	return out
}

// SendAll 逐渠道发送，返回 {渠道: 结果}。单渠道失败不影响其他渠道。
func SendAll(ctx context.Context, cfg Config, msg Message) map[string]string {
	results := map[string]string{}
	for _, ch := range EnabledChannels(cfg) {
		if err := send(ctx, cfg, ch, msg); err != nil {
			results[ch] = fmt.Sprintf("failed: %v", err)
			continue
		}
		results[ch] = "ok"
	}
	return results
}

// ResultText 把发送结果翻成一句可读的话：失败要点名，全没启用时返回空串
// （什么都没发就不在事件里占一行）。
func ResultText(results map[string]string, what string) string {
	if what == "" {
		what = "报告"
	}
	var failed []string
	for ch, state := range results {
		if state != "ok" {
			failed = append(failed, ch)
		}
	}
	if len(failed) > 0 {
		return what + "发送失败：" + strings.Join(failed, "、")
	}
	if len(results) == 0 {
		return ""
	}
	return what + "已发送"
}

func send(ctx context.Context, cfg Config, channel string, msg Message) error {
	switch channel {
	case "telegram":
		return sendTelegram(ctx, cfg, msg)
	case "email":
		return sendEmail(ctx, cfg, msg)
	case "webhook":
		return sendWebhook(ctx, cfg, msg)
	default:
		return fmt.Errorf("未知通知渠道 %s", channel)
	}
}

// --- Telegram ----------------------------------------------------------------

const tgLimit = 4000 // Telegram 单条上限 4096，留余量

var tagRe = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</p>", "\n")
	return tagRe.ReplaceAllString(s, "")
}

func sanitizeTG(s string) string {
	r := strings.NewReplacer(
		"<br>", "\n",
		"<br/>", "\n",
		"<br />", "\n",
		"</p>", "\n",
		"<p>", "",
	)
	return r.Replace(s)
}

// clipTG 按行截断：直接切字符串会把 <b>/<code> 标签切成两半导致 Telegram 400。
// 预算预留截断后缀的字节，保证截断后整体仍不超限。
func clipTG(text string) string {
	if len(text) <= tgLimit {
		return text
	}
	suffix := "\n…（内容过长已截断）"
	var kept []string
	size := 0
	for _, line := range strings.Split(text, "\n") {
		if size+len(line)+1 > tgLimit-len(suffix)-60 {
			break
		}
		kept = append(kept, line)
		size += len(line) + 1
	}
	return strings.Join(kept, "\n") + suffix
}

func tgClient(proxy string) (*http.Client, error) {
	tr := &http.Transport{}
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("代理地址无效: %w", err)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: tr}, nil
}

func sendTelegram(ctx context.Context, cfg Config, msg Message) error {
	token, chatID := cfg.get("notify_tg_token"), cfg.get("notify_tg_chat_id")
	if token == "" || chatID == "" {
		return fmt.Errorf("Telegram token 或 chat_id 未配置")
	}
	client, err := tgClient(cfg.get("notify_tg_proxy"))
	if err != nil {
		return err
	}
	base := cfg.get("notify_tg_api_base") // 自建 Bot API 反代可覆盖
	if base == "" {
		base = "https://api.telegram.org"
	}
	url := strings.TrimRight(base, "/") + "/bot" + token + "/sendMessage"

	tgText := sanitizeTG(msg.HTML)
	if strings.TrimSpace(tgText) == "" {
		tgText = msg.Text
	}
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     clipTG(tgText),
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		raw, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, trunc(string(body), 200))
			// 400 通常是 HTML 解析问题：去掉 parse_mode 用纯文本再试一次
			if resp.StatusCode == 400 && payload["parse_mode"] == "HTML" {
				payload["text"] = stripTags(payload["text"].(string))
				delete(payload, "parse_mode")
				continue
			}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				break // 客户端错误重试无意义
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("Telegram 推送失败：%v", lastErr)
}

// --- 邮件（SMTP）--------------------------------------------------------------

func sendEmail(ctx context.Context, cfg Config, msg Message) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	host := cfg.get("notify_mail_host")
	port := strings.TrimSpace(cfg["notify_mail_port"])
	if port == "" {
		port = "465"
	}
	user, pass := cfg.get("notify_mail_username"), cfg["notify_mail_password"]
	to := cfg.get("notify_mail_to")
	from := user
	if from == "" {
		from = to // 很多 SMTP 拒绝空 From；用收件人兜底
	}
	brand := cfg.get("brand")
	if brand == "" {
		brand = "CDT 管理台"
	}
	addr := host + ":" + port
	deadline, _ := ctx.Deadline()
	dialer := net.Dialer{Timeout: 15 * time.Second, Deadline: deadline}

	sec := strings.ToLower(cfg.get("notify_mail_security"))
	var cl *smtp.Client
	var err error
	if sec == "ssl" || port == "465" { // 隐式 TLS
		var tconn *tls.Conn
		tconn, err = tls.DialWithDialer(&dialer, "tcp", addr, &tls.Config{ServerName: host})
		if err != nil {
			return fmt.Errorf("SMTP 连接失败: %w", err)
		}
		cl, err = smtp.NewClient(tconn, host)
	} else { // 明文端口，可选 STARTTLS
		var pconn net.Conn
		pconn, err = dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("SMTP 连接失败: %w", err)
		}
		cl, err = smtp.NewClient(pconn, host)
		if err == nil && (sec == "tls" || sec == "starttls") {
			err = cl.StartTLS(&tls.Config{ServerName: host})
		}
	}
	if err != nil {
		cl.Close()
		return fmt.Errorf("SMTP 会话失败: %w", err)
	}
	defer cl.Close()

	if pass != "" {
		if err := cl.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := cl.Mail(from); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	subject := mime.QEncoding.Encode("utf-8", brand+" · "+msg.Title)
	fromHdr := fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("utf-8", brand), from)
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=\"utf-8\"\r\n\r\n", fromHdr, to, subject)
	if _, err := w.Write([]byte(headers + renderEmailHTML(brand, msg))); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func renderEmailHTML(brand string, msg Message) string {
	contentHTML := msg.HTML
	if strings.TrimSpace(contentHTML) == "" {
		contentHTML = "<pre style=\"font-family: inherit; margin: 0; white-space: pre-wrap;\">" + msg.Text + "</pre>"
	} else {
		contentHTML = strings.ReplaceAll(contentHTML, "\n", "<br>")
	}

	escBrand := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;").Replace(brand)

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin:0;padding:24px 12px;background-color:#f6f8fa;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#24292f;line-height:1.6;">
  <div style="max-width:580px;margin:0 auto;background:#ffffff;border:1px solid #d0d7de;border-radius:10px;overflow:hidden;box-shadow:0 1px 3px rgba(0,0,0,0.06);">
    <div style="background:#0969da;color:#ffffff;padding:16px 20px;font-size:16px;font-weight:600;">
      %s
    </div>
    <div style="padding:24px 20px;font-size:14px;color:#1f2328;">
      %s
    </div>
    <div style="background:#f6f8fa;border-top:1px solid #d0d7de;padding:12px 20px;font-size:12px;color:#656d76;text-align:center;">
      本邮件由 %s 自动发送，请勿直接回复
    </div>
  </div>
</body>
</html>`, escBrand, contentHTML, escBrand)
}

// --- Webhook ------------------------------------------------------------------

// jsonInner 把值转成可安全嵌入 JSON 字符串字面量的形态（去首尾引号）：
// 自由文本含双引号/换行时不会产出非法 JSON。
func jsonInner(v string) string {
	b, _ := json.Marshal(v)
	return string(b[1 : len(b)-1])
}

func sendWebhook(ctx context.Context, cfg Config, msg Message) error {
	endpoint := cfg.get("notify_wh_url")
	method := strings.ToUpper(cfg.get("notify_wh_method"))
	if method == "" {
		method = http.MethodPost
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if raw := cfg.get("notify_wh_headers"); raw != "" {
		var extra map[string]string
		if err := json.Unmarshal([]byte(raw), &extra); err == nil {
			for k, v := range extra {
				headers[k] = v
			}
		} // 非法 JSON 的自定义 headers 忽略，不阻断
	}

	var body []byte
	if tpl := cfg["notify_wh_body"]; strings.TrimSpace(tpl) == "" {
		body, _ = json.Marshal(map[string]string{
			"title": msg.Title, "text": msg.Text, "kind": msg.Kind,
		})
	} else {
		out := strings.NewReplacer(
			"{title}", jsonInner(msg.Title),
			"{text}", jsonInner(msg.Text),
			"{kind}", jsonInner(msg.Kind),
		).Replace(tpl)
		body = []byte(out)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("Webhook 返回 HTTP %d", resp.StatusCode)
	}
	return nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
