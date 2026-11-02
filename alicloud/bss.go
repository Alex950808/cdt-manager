package alicloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// --- BSS 费用中心 OpenAPI 2017-12-14（RPC 签名，账号级慢数据） ----------------
//
// 覆盖：
//	QueryAccountBalance   账户可用余额
//	DescribeInstanceBill  实例账单明细（税前金额汇总）
//
// 站点说明：RAM 子账号绑定站点，中国站 AK 打国际站域名（或反之）会直接报
// AuthSiteFail 类错误。AccountBalance / InstanceBill 提供站点自动纠正：
// 先按给定站点调用，命中站点错误后换另一站点重试一次，并把**实际生效**的
// 站点返回给调用方。
//
// 所需 RAM 权限：AliyunBSSReadOnlyAccess（或 bss:QueryAccountBalance /
// bss:DescribeInstanceBill）。

const bssVersion = "2017-12-14"

// Site 表示阿里云账号所属站点，决定 BSS 费用接口的接入点。
type Site string

const (
	SiteChina         Site = "china"         // 中国站（默认）
	SiteInternational Site = "international" // 国际站
)

// ParseSite 把字符串归一为站点，未知值回落中国站。
func ParseSite(s string) Site {
	if Site(s) == SiteInternational {
		return SiteInternational
	}
	return SiteChina
}

// Opposite 返回另一站点。
func (s Site) Opposite() Site {
	if s == SiteInternational {
		return SiteChina
	}
	return SiteInternational
}

// endpoint 返回该站点 BSS 接口的 (region, domain)。
func (s Site) endpoint() (region, domain string) {
	if s == SiteInternational {
		return "ap-southeast-1", "business.ap-southeast-1.aliyuncs.com"
	}
	return "cn-hangzhou", "business.aliyuncs.com"
}

// Balance 是账户余额快照。Site 为实际生效站点（可能被自动纠正过）。
type Balance struct {
	AvailableAmount float64
	Currency        string
	Site            Site
}

// BillSummary 是一台实例某账期的消费汇总。
type BillSummary struct {
	Total   float64 // 税前金额合计（PretaxAmount 累加）
	Cycle   string  // YYYY-MM
	Site    Site    // 实际生效站点
	Details int     // 参与汇总的明细行数（实例 / 云盘 / 带宽等分开计行）
}

// bssEnvelope 是 BSS 响应顶层的公共字段。
type bssEnvelope struct {
	Code      string `json:"Code"`
	Message   string `json:"Message"`
	RequestID string `json:"RequestId"`
}

// codeEnvelope 让各 BSS 响应结构暴露顶层业务码，供统一校验。
type codeEnvelope interface{ codeEnvelope() *bssEnvelope }

// bssInvoke 统一发起 BSS 请求：解码到 out 后校验顶层业务码——
// BSS 在业务失败时可能仍返回 HTTP 200，仅凭状态码不够。
func (c *Client) bssInvoke(ctx context.Context, site Site, action string, params map[string]string, out codeEnvelope) error {
	_, domain := site.endpoint()
	if err := c.call(ctx, httpPost, domain, params, out); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return fmt.Errorf("BSS %s: %w", action, apiErr)
		}
		return fmt.Errorf("BSS %s: %w", action, err)
	}
	if env := out.codeEnvelope(); env.Code != "" && env.Code != "Success" && env.Code != "200" {
		return &APIError{HTTPStatus: 200, Code: env.Code, Message: env.Message, RequestID: env.RequestID}
	}
	return nil
}

// --- 账户余额 ----------------------------------------------------------------

type queryAccountBalanceResponse struct {
	bssEnvelope
	Data struct {
		AvailableAmount flexFloat `json:"AvailableAmount"`
		Currency        string    `json:"Currency"`
	} `json:"Data"`
}

func (r *queryAccountBalanceResponse) codeEnvelope() *bssEnvelope { return &r.bssEnvelope }

// QueryAccountBalance 查询账户可用额度（不缓存、不纠正站点）。
func (c *Client) QueryAccountBalance(ctx context.Context, site Site) (*Balance, error) {
	params := map[string]string{
		"Action":  "QueryAccountBalance",
		"Version": bssVersion,
	}
	var out queryAccountBalanceResponse
	if err := c.bssInvoke(ctx, site, "QueryAccountBalance", params, &out); err != nil {
		return nil, err
	}
	curr := out.Data.Currency
	if curr == "" {
		if site == SiteInternational {
			curr = "USD"
		} else {
			curr = "CNY"
		}
	}
	return &Balance{
		AvailableAmount: float64(out.Data.AvailableAmount),
		Currency:        curr,
		Site:            site,
	}, nil
}

// AccountBalance 查询账户余额：带 TTL 缓存（若启用）与站点自动纠正。
// 返回的 Balance.Site 为实际生效的站点。
func (c *Client) AccountBalance(ctx context.Context, site Site) (*Balance, error) {
	fetch := func(s Site) (*Balance, error) { return c.QueryAccountBalance(ctx, s) }
	if c.balanceData != nil && c.balanceTTL > 0 {
		key := balanceCacheKey(c.AccessKeyID, site)
		if b, ok := c.balanceData.get(key, c.balanceTTL, c.now); ok {
			return &b, nil
		}
		b, _, err := withSiteFallback(ctx, site, fetch)
		if err != nil {
			return nil, err
		}
		c.balanceData.put(key, *b, c.now)
		return b, nil
	}
	b, _, err := withSiteFallback(ctx, site, fetch)
	return b, err
}

// --- 实例账单 ----------------------------------------------------------------

type billItemRaw struct {
	InstanceID       string    `json:"InstanceID"`
	ProductCode      string    `json:"ProductCode"`
	ProductType      string    `json:"ProductType"`
	SubscriptionType string    `json:"SubscriptionType"`
	PretaxAmount     flexFloat `json:"PretaxAmount"`
}

// itemsShape 兼容两种明细形态：{"Item":[...]}（常见包装）与直接数组。
type itemsShape struct {
	Item []billItemRaw
}

func (it *itemsShape) UnmarshalJSON(data []byte) error {
	var wrapped struct {
		Item []billItemRaw `json:"Item"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil {
		it.Item = wrapped.Item
		return nil
	}
	return json.Unmarshal(data, &it.Item)
}

type describeInstanceBillResponse struct {
	bssEnvelope
	Data struct {
		BillingCycle string     `json:"BillingCycle"`
		NextToken    string     `json:"NextToken"`
		TotalCount   int        `json:"TotalCount"`
		Items        itemsShape `json:"Items"`
	} `json:"Data"`
}

func (r *describeInstanceBillResponse) codeEnvelope() *bssEnvelope { return &r.bssEnvelope }

// InstanceBill 汇总一台实例在指定账期（YYYY-MM）的税前消费。
//
// 明细按实例 / 云盘 / 带宽分行返回，这里逐页累加 PretaxAmount 得到总额；
// 站点错误自动换站重试，BillSummary.Site 为实际生效站点。
func (c *Client) InstanceBill(ctx context.Context, site Site, cycle, instanceID string) (*BillSummary, error) {
	bill, _, err := withSiteFallback(ctx, site, func(s Site) (*BillSummary, error) {
		return c.instanceBillOnSite(ctx, s, cycle, instanceID)
	})
	return bill, err
}

func (c *Client) instanceBillOnSite(ctx context.Context, site Site, cycle, instanceID string) (*BillSummary, error) {
	const maxPages = 50
	summary := &BillSummary{Cycle: cycle, Site: site}
	nextToken := ""
	for page := 0; ; page++ {
		params := map[string]string{
			"Action":       "DescribeInstanceBill",
			"Version":      bssVersion,
			"BillingCycle": cycle,
			"InstanceID":   instanceID,
			"Granularity":  "MONTHLY",
			"MaxResults":   "300",
		}
		if nextToken != "" {
			params["NextToken"] = nextToken
		}
		var out describeInstanceBillResponse
		if err := c.bssInvoke(ctx, site, "DescribeInstanceBill", params, &out); err != nil {
			return nil, err
		}
		for _, item := range out.Data.Items.Item {
			summary.Total += float64(item.PretaxAmount)
			summary.Details++
		}
		if out.Data.NextToken == "" || page >= maxPages {
			return summary, nil
		}
		nextToken = out.Data.NextToken
	}
}

// withSiteFallback 以给定站点执行 fn；命中站点不匹配错误时换另一站点重试一次。
// 返回 (结果, 实际生效站点, 错误)。换站也失败时保留**原始错误**——它更能说明
// 真实问题（通常是权限缺失），而不是换站后的次生错误。
func withSiteFallback[T any](ctx context.Context, site Site, fn func(Site) (T, error)) (T, Site, error) {
	var zero T
	v, err := fn(site)
	if err == nil {
		return v, site, nil
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.IsSiteMismatch() {
		return zero, site, err
	}
	other := site.Opposite()
	v2, err2 := fn(other)
	if err2 == nil {
		return v2, other, nil
	}
	return zero, site, err
}

func (b *BillSummary) Round() {
	b.Total = math.Round(b.Total*100) / 100
}
