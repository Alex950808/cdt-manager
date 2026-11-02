package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBase = "https://api.cloudflare.com"

// Client 是 Cloudflare API v4 客户端（DNS DDNS 最小集合）。
// 支持两种认证模式：API Token（Bearer，推荐）与 Global Key（X-Auth-Email/X-Auth-Key）。
type Client struct {
	Token     string // API Token，需 Zone:Read + DNS:Edit 权限
	Email     string // Global Key 模式：账号邮箱
	GlobalKey string // Global Key 模式：账户级 Global API Key
	http      *http.Client
	baseURL   string // 可覆盖用于测试
}

// Option 定制客户端。
type Option func(*Client)

// WithHTTPClient 使用自定义 HTTP 客户端（代理 / 测试改写等）。
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithGlobalKey 使用 Global Key 认证（覆盖 Token 模式）。
func WithGlobalKey(email, key string) Option {
	return func(c *Client) { c.Email, c.GlobalKey = email, key }
}

// WithBaseURL 覆盖 API 根地址（本地 mock / 测试）。
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// Timeout 定制请求超时。
func Timeout(d time.Duration) Option {
	return func(c *Client) {
		if c.http.Timeout == 0 {
			c.http.Timeout = d
		}
	}
}

func New(token string, opts ...Option) *Client {
	c := &Client{Token: token, http: &http.Client{Timeout: 15 * time.Second}, baseURL: defaultBase}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError 是 Cloudflare 返回的业务错误（HTTP 4xx/5xx 或 success=false）。
type APIError struct {
	HTTPStatus int
	Code       string // Cloudflare 错误码（数字字符串）或 "cf_error"
	Message    string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("cloudflare: %s: %s", e.Code, e.Message)
}

// Record 是一条 DNS 记录的最小视图。
type Record struct {
	ID      string
	Type    string // "A"
	Name    string // 完整域名，如 ddns.example.com
	Content string // A 记录的 IP
	TTL     int    // 1 = auto
	Proxied bool   // 是否橙云代理；DDNS 直连场景通常 false
}

// --- v4 响应骨架 -------------------------------------------------------------

type cfEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

// do 发起一次 API 调用并解析 v4 信封；success=false 或非 2xx 归一为 *APIError。
func (c *Client) do(ctx context.Context, method, path string, body any, out *json.RawMessage) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cloudflare: 编码请求失败: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/client/v4"+path, reader)
	if err != nil {
		return fmt.Errorf("cloudflare: 构造请求失败: %w", err)
	}
	if c.GlobalKey != "" && c.Email != "" {
		req.Header.Set("X-Auth-Email", c.Email)
		req.Header.Set("X-Auth-Key", c.GlobalKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: 网络错误: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("cloudflare: 读取响应失败: %w", err)
	}

	var env cfEnvelope
	_ = json.Unmarshal(raw, &env) // 非 JSON 响应（网关错误页）也走下面的状态码分支
	if resp.StatusCode >= 400 || !env.Success {
		e := &APIError{HTTPStatus: resp.StatusCode, Code: "cf_error", Message: strings.TrimSpace(string(raw))}
		if len(env.Errors) > 0 {
			e.Code = fmt.Sprint(env.Errors[0].Code)
			e.Message = env.Errors[0].Message
		}
		return e
	}
	if out != nil {
		*out = env.Result
	}
	return nil
}

// ZoneID 按名称精确取 Zone ID（找不到返回 "" 与 nil —— 调用方据此报配置错误）。
func (c *Client) ZoneID(ctx context.Context, zoneName string) (string, error) {
	var result json.RawMessage
	path := "/zones?name=" + urlQueryEscape(zoneName)
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return "", err
	}
	var zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(result, &zones); err != nil {
		return "", fmt.Errorf("cloudflare: 解析 zones 失败: %w", err)
	}
	for _, z := range zones {
		if strings.EqualFold(z.Name, zoneName) {
			return z.ID, nil
		}
	}
	return "", nil
}

// FindRecord 查找指定类型与名称的记录；不存在返回 nil（不视为错误）。
func (c *Client) FindRecord(ctx context.Context, zoneID, recordType, name string) (*Record, error) {
	var result json.RawMessage
	path := "/zones/" + zoneID + "/dns_records?type=" + recordType + "&name=" + urlQueryEscape(name)
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	var records []Record
	if err := json.Unmarshal(result, &records); err != nil {
		return nil, fmt.Errorf("cloudflare: 解析 dns_records 失败: %w", err)
	}
	for i := range records {
		if strings.EqualFold(records[i].Name, name) {
			return &records[i], nil
		}
	}
	return nil, nil
}

// UpdateRecord 更新记录内容（PUT 全量字段）。
func (c *Client) UpdateRecord(ctx context.Context, zoneID, recordID string, rec Record) error {
	var result json.RawMessage
	body := map[string]any{
		"type":    rec.Type,
		"name":    rec.Name,
		"content": rec.Content,
		"ttl":     rec.TTL,
		"proxied": rec.Proxied,
	}
	return c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+recordID, body, &result)
}

// UpsertA 把 A 记录 {name} 指向 {ip}：查询现值，相同则跳过（返回 changed=false），
// 不同则 PUT 更新；记录不存在时返回错误（语义与"显式绑定"一致——不擅自新建）。
func (c *Client) UpsertA(ctx context.Context, zoneName, name, ip string) (changed bool, err error) {
	zoneID, err := c.ZoneID(ctx, zoneName)
	if err != nil {
		return false, err
	}
	if zoneID == "" {
		return false, &APIError{Code: "zone_not_found", Message: "zone 不存在或 token 无权访问: " + zoneName}
	}
	rec, err := c.FindRecord(ctx, zoneID, "A", name)
	if err != nil {
		return false, err
	}
	if rec == nil {
		return false, &APIError{Code: "record_not_found", Message: "A 记录不存在（请在 Cloudflare 预先创建）: " + name}
	}
	if rec.Content == ip {
		return false, nil
	}
	rec.Content = ip
	if err := c.UpdateRecord(ctx, zoneID, rec.ID, *rec); err != nil {
		return false, err
	}
	return true, nil
}

// urlQueryEscape 极简查询串编码（zone/record 名均为受限字符集）。
func urlQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return b.String()
}
