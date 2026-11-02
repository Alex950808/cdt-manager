package alicloud

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Client 是面向全部已覆盖接口的共享客户端：持有凭证与 HTTP 配置，
// 并负责公共参数组装、签名、发送与错误归一。
type Client struct {
	AccessKeyID     string
	AccessKeySecret string

	http        *http.Client
	now         func() time.Time // 可注入时钟（测试用）
	nonce       func() string    // 可注入随机数源（测试用）
	baseURL     string           // 默认 "https://"，可覆盖为任意端点（本地 mock / 网关）
	balanceTTL  time.Duration    // >0 时启用余额 TTL 缓存
	balanceData *balanceCache
}

// Option 定制 Client 行为。
type Option func(*Client)

// WithHTTPClient 使用自定义 *http.Client（含代理、TLS 等配置）。
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithTimeout 设置默认 HTTP 超时（未传 WithHTTPClient 时生效）。
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.http.Timeout == 0 || c.http.Timeout == defaultTimeout {
			c.http.Timeout = d
		}
	}
}

// WithBalanceCache 启用余额 TTL 缓存：同一 AK+站点在 TTL 内直接复用上次结果。
// 余额是慢变量，避免对账场景反复打接口。
func WithBalanceCache(ttl time.Duration) Option {
	return func(c *Client) { c.balanceTTL = ttl; c.balanceData = newBalanceCache() }
}

// WithBaseURL 覆盖默认的 https:// 端点前缀，用于本地 mock 或网关场景。
// 传入形如 "http://127.0.0.1:8123"（结尾斜杠可省略），
// 最终请求 URL = base + 产品域名 + "/?" + 签名查询串。
func WithBaseURL(base string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(base, "/") + "/" }
}

const defaultTimeout = 15 * time.Second

// New 创建客户端；凭证建议使用 RAM 子账号的 AccessKey。
func New(accessKeyID, accessKeySecret string, opts ...Option) *Client {
	c := &Client{
		AccessKeyID:     accessKeyID,
		AccessKeySecret: accessKeySecret,
		http:            &http.Client{Timeout: defaultTimeout},
		baseURL:         "https://",
		now:             time.Now,
		nonce:           randomNonce,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// randomNonce 生成 UUID v4 形态的签名随机数（协议要求每次请求唯一）。
func randomNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败极罕见；退化为时间熵，保证签名流程不中断
		return fmt.Sprintf("nonce-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0F) | 0x40 // version 4
	b[8] = (b[8] & 0x3F) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// call 执行一次签名 RPC 调用并把 JSON 响应解码到 out（nil 表示不关心响应体）。
func (c *Client) call(ctx context.Context, method, domain string, params map[string]string, out any) error {
	if params == nil {
		params = map[string]string{}
	}
	c.withCommonParams(params)

	sts := stringToSign(method, canonicalQuery(params))
	params["Signature"] = rpcSignature(c.AccessKeySecret, sts)

	url := c.baseURL + domain + "/?" + finalQuery(params)

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return fmt.Errorf("alicloud: 构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cdt-manager-go/1.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("alicloud: 网络错误 (%s): %w", domain, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8MB 上限，防异常响应撑爆内存
	if err != nil {
		return fmt.Errorf("alicloud: 读取响应失败: %w", err)
	}

	if resp.StatusCode >= 400 {
		return statusError(resp, body)
	}
	// 不关心响应体时，仍兜底检测 HTTP 200 直出的错误体（部分网关行为）
	if out == nil {
		if apiErr := parseErrorBody(body); apiErr.Code != "" &&
			apiErr.Code != "Unknown" && apiErr.Code != "Success" {
			apiErr.HTTPStatus = resp.StatusCode
			return apiErr
		}
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		// 网关直出 XML 错误体时 HTTP 可能仍是 200，按错误解析给出可读信息
		if apiErr := parseErrorBody(body); apiErr.Code != "Unknown" {
			apiErr.HTTPStatus = resp.StatusCode
			return apiErr
		}
		return fmt.Errorf("alicloud: 响应解析失败 (%s): %w", domain, err)
	}
	return nil
}

// withCommonParams 写入 RPC 协议公共参数（业务参数之外的固定集合）。
func (c *Client) withCommonParams(params map[string]string) {
	params["AccessKeyId"] = c.AccessKeyID
	params["Format"] = "JSON"
	params["SignatureMethod"] = "HMAC-SHA1"
	params["SignatureVersion"] = "1.0"
	params["SignatureNonce"] = c.nonce()
	// 协议要求 UTC ISO8601、精确到秒、以 Z 结尾
	params["Timestamp"] = c.now().UTC().Format("2006-01-02T15:04:05") + "Z"
}

// finalQuery 生成最终发送用的完整查询串：所有键值（含 Signature）统一走
// percentEncode，与签名时的编码保持字节级一致。
func finalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(percentEncode(k))
		b.WriteByte('=')
		b.WriteString(percentEncode(params[k]))
	}
	return b.String()
}
