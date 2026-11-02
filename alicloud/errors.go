package alicloud

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

// APIError 表示阿里云端返回的错误（HTTP >= 400，或业务 Code 非成功）。
type APIError struct {
	HTTPStatus int    // 响应 HTTP 状态码；业务层错误（HTTP 200 但 Code!=Success）时为 200
	Code       string // 如 InvalidAccessKeyId.NotFound、AuthSiteFail、SignatureDoesNotMatch
	Message    string
	RequestID  string
	HostID     string
	Recommend  string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "alicloud: %s", e.Code)
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (requestId=%s)", e.RequestID)
	}
	return b.String()
}

// IsCode 报告错误码是否与 code 一致（忽略大小写）。
func (e *APIError) IsCode(code string) bool {
	return e != nil && strings.EqualFold(e.Code, code)
}

// IsInsufficientInventory 报告是否为抢占式实例库存不足（启动失败常见原因）。
func (e *APIError) IsInsufficientInventory() bool {
	return e.IsCode("InsufficientInventory")
}

// siteMismatchCodes：RAM 子账号与站点不绑定时，BSS 网关返回的典型错误码。
// 选错站点（如用中国站 AK 打国际站域名）会命中其中之一。
var siteMismatchCodes = map[string]bool{
	"AuthSiteFail":  true,
	"NotApplicable": true,
	"AuthPrincipal": true,
}

// IsSiteMismatch 报告该错误是否为「站点不匹配」——调用方可以据此换站点重试。
// 个别网关版本不在 Code 里体现，而是在 Message 中带 "site matches" 字样，一并识别。
func (e *APIError) IsSiteMismatch() bool {
	if e == nil {
		return false
	}
	if siteMismatchCodes[e.Code] {
		return true
	}
	return strings.Contains(e.Message, "site matches")
}

// --- 错误响应体解析 -------------------------------------------------------
//
// 阿里云 RPC 接口的错误体可能是 JSON（常见）或 XML（网关直出的部分错误），
// 这里两者都兜住；都解析不出来时至少保留原始片段。

type jsonErrorBody struct {
	Code      string `json:"Code"`
	Message   string `json:"Message"`
	RequestID string `json:"RequestId"`
	HostID    string `json:"HostId"`
	Recommend string `json:"Recommend"`
}

type xmlErrorBody struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	RequestID string   `xml:"RequestId"`
	HostID    string   `xml:"HostId"`
	Recommend string   `xml:"Recommend"`
}

// parseErrorBody 尽力从响应体中提取结构化错误信息。
func parseErrorBody(body []byte) *APIError {
	var je jsonErrorBody
	if err := json.Unmarshal(body, &je); err == nil && je.Code != "" {
		return &APIError{
			Code:      je.Code,
			Message:   je.Message,
			RequestID: je.RequestID,
			HostID:    je.HostID,
			Recommend: je.Recommend,
		}
	}
	var xe xmlErrorBody
	if err := xml.Unmarshal(body, &xe); err == nil && xe.Code != "" {
		return &APIError{
			Code:      xe.Code,
			Message:   xe.Message,
			RequestID: xe.RequestID,
			HostID:    xe.HostID,
			Recommend: xe.Recommend,
		}
	}
	return &APIError{Code: "Unknown", Message: strings.TrimSpace(string(body))}
}

// statusError 把一次 HTTP 响应整体转成 APIError。
func statusError(resp *http.Response, body []byte) *APIError {
	e := parseErrorBody(body)
	e.HTTPStatus = resp.StatusCode
	if e.HostID == "" {
		e.HostID = resp.Header.Get("X-Acs-Host-Id")
	}
	return e
}
