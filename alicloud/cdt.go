package alicloud

import (
	"context"
	"fmt"
)

// --- CDT OpenAPI 2021-08-13（RPC 签名，中心化域名） --------------------------
//
// CDT（云企业网流量/数据传输）是账号级中心化服务，与实例所在地域无关，
// 统一走 cdt.aliyuncs.com（固定 cn-hongkong 计量点）。
//
// 覆盖：
//	ListCdtInternetTraffic  互联网流量明细（按实例/产品逐条返回）

const (
	cdtVersion = "2021-08-13"
	cdtDomain  = "cdt.aliyuncs.com"
	cdtAction  = "ListCdtInternetTraffic"
)

// TrafficDetail 是一条互联网流量明细。Traffic 单位为字节。
type TrafficDetail struct {
	CdtID        string
	InstanceID   string
	ProductType  string
	TrafficType  string
	TrafficBytes float64
	UpdateTime   string
}

type cdtTrafficRaw struct {
	CdtID       string  `json:"CdtId"`
	InstanceID  string  `json:"InstanceId"`
	ProductType string  `json:"ProductType"`
	TrafficType string  `json:"TrafficType"`
	Traffic     float64 `json:"Traffic"`
	UpdateTime  string  `json:"UpdateTime"`
}

type listCdtInternetTrafficResponse struct {
	RequestID      string          `json:"RequestId"`
	NextToken      string          `json:"NextToken"`
	TrafficDetails []cdtTrafficRaw `json:"TrafficDetails"`
}

// ListCdtInternetTraffic 拉取当前计费周期的全部互联网流量明细。
//
// 服务端返回 NextToken 时自动翻页直到取尽（上限 50 页防御性兜底）。
func (c *Client) ListCdtInternetTraffic(ctx context.Context) ([]TrafficDetail, error) {
	const maxPages = 50
	var all []TrafficDetail
	nextToken := ""
	for page := 0; ; page++ {
		params := map[string]string{
			"Action":  cdtAction,
			"Version": cdtVersion,
		}
		if nextToken != "" {
			params["NextToken"] = nextToken
		}
		var out listCdtInternetTrafficResponse
		if err := c.call(ctx, httpPost, cdtDomain, params, &out); err != nil {
			return nil, fmt.Errorf("CDT ListCdtInternetTraffic: %w", err)
		}
		for _, raw := range out.TrafficDetails {
			all = append(all, TrafficDetail{
				CdtID:        raw.CdtID,
				InstanceID:   raw.InstanceID,
				ProductType:  raw.ProductType,
				TrafficType:  raw.TrafficType,
				TrafficBytes: raw.Traffic,
				UpdateTime:   raw.UpdateTime,
			})
		}
		if out.NextToken == "" || page >= maxPages {
			return all, nil
		}
		nextToken = out.NextToken
	}
}

// InternetTrafficGB 汇总当月出方向互联网流量（GB）。
//
// 返回 (nil, nil) 表示接口调用成功但一条明细都没有——月初流量包重置后确实为 0，
// 接口抖动返回空列表时也一样，两者在这一层无法区分，交由调用方结合上次读数判断，
// 这里不擅自折算成 0。
func (c *Client) InternetTrafficGB(ctx context.Context) (*float64, error) {
	details, err := c.ListCdtInternetTraffic(ctx)
	if err != nil {
		return nil, err
	}
	if len(details) == 0 {
		return nil, nil
	}
	gb := GB(details)
	return &gb, nil
}

// GB 把字节流量汇总换算为 GB（1024 进制）。
func GB(details []TrafficDetail) float64 {
	var total float64
	for _, d := range details {
		total += d.TrafficBytes
	}
	return total / (1 << 30)
}
