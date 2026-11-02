package alicloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// rewriteTransport 把发往阿里云域名的请求透明改写到本地测试服务器，
// 让签名、参数、域名拼装全链路都走真实代码路径。
type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	return t.base.RoundTrip(clone)
}

// newTestClient 返回指向本地服务器的客户端；固定时钟与随机数使请求可复现。
func newTestClient(t *testing.T, ts *httptest.Server, opts ...Option) *Client {
	t.Helper()
	target, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	c := New("testid", "testsecret", append([]Option{WithHTTPClient(&http.Client{
		Transport: &rewriteTransport{target: target, base: http.DefaultTransport},
	})}, opts...)...)
	c.now = func() time.Time { return time.Date(2024, 6, 1, 8, 0, 0, 0, time.UTC) }
	c.nonce = func() string { return "nonce-fixed" }
	return c
}

// parseQueryOf 从请求中解析查询串（POST 时参数在 URL query 上）。
func parseQueryOf(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		t.Fatalf("bad query %q: %v", r.URL.RawQuery, err)
	}
	return q
}

// verifySignature 在服务端用收到的参数（去掉 Signature）独立重算签名，
// 与请求携带的 Signature 比对——覆盖从参数组装到编码到签名的全链路。
func verifySignature(t *testing.T, q url.Values) {
	t.Helper()
	params := map[string]string{}
	for k := range q {
		params[k] = q.Get(k)
	}
	got := params["Signature"]
	delete(params, "Signature")
	want := rpcSignature("testsecret", stringToSign("POST", canonicalQuery(params)))
	if got != want {
		t.Errorf("server-side signature mismatch:\n got  %s\n want %s", got, want)
	}
}

func TestCallSignsAndSendsCommonParams(t *testing.T) {
	var gotQuery url.Values
	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotQuery = parseQueryOf(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"RequestId":"req-1"}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	var out struct {
		RequestID string `json:"RequestId"`
	}
	err := c.call(context.Background(), httpPost, "ecs.cn-hongkong.aliyuncs.com", map[string]string{
		"Action":  "DescribeInstances",
		"Version": ecsVersion,
	}, &out)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.RequestID != "req-1" {
		t.Errorf("RequestID = %q, want req-1", out.RequestID)
	}

	if gotMethod != httpPost || gotPath != "/" {
		t.Errorf("method/path = %s %s, want POST /", gotMethod, gotPath)
	}
	for k, want := range map[string]string{
		"Action":           "DescribeInstances",
		"Version":          ecsVersion,
		"AccessKeyId":      "testid",
		"Format":           "JSON",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   "nonce-fixed",
		"Timestamp":        "2024-06-01T08:00:00Z",
	} {
		if gotQuery.Get(k) != want {
			t.Errorf("param %s = %q, want %q", k, gotQuery.Get(k), want)
		}
	}
	if gotQuery.Get("Signature") == "" {
		t.Error("Signature param missing")
	}
	verifySignature(t, gotQuery)
}

func TestCallParsesJSONErrorOn4xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"Code":"InvalidAccessKeyId.NotFound","Message":"The AccessKeyId provided does not exist.","RequestId":"r-2","HostId":"ecs.cn-hongkong.aliyuncs.com","Recommend":"https://error.aliyun.com"}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	err := c.call(context.Background(), httpPost, "ecs.cn-hongkong.aliyuncs.com", map[string]string{"Action": "X"}, nil)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.HTTPStatus != 400 || !apiErr.IsCode("InvalidAccessKeyId.NotFound") {
		t.Errorf("unexpected error: %+v", apiErr)
	}
	if apiErr.RequestID != "r-2" || apiErr.HostID != "ecs.cn-hongkong.aliyuncs.com" {
		t.Errorf("requestId/hostId not parsed: %+v", apiErr)
	}
}

func TestCallParsesXMLErrorOn200(t *testing.T) {
	// 部分网关错误以 HTTP 200 + XML 直出
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<Error><Code>SignatureDoesNotMatch</Code><Message>The signature does not match.</Message><RequestId>r-3</RequestId></Error>`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	err := c.call(context.Background(), httpPost, "ecs.test.aliyuncs.com", map[string]string{"Action": "X"}, nil)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if !apiErr.IsCode("SignatureDoesNotMatch") || apiErr.HTTPStatus != 200 {
		t.Errorf("unexpected error: %+v", apiErr)
	}
}

func TestAPErrorSiteMismatch(t *testing.T) {
	cases := []struct {
		code, message string
		want          bool
	}{
		{"AuthSiteFail", "", true},
		{"NotApplicable", "", true},
		{"AuthPrincipal", "", true},
		{"", "The request site matches no account.", true},
		{"InvalidAction", "nope", false},
	}
	for _, tc := range cases {
		e := &APIError{Code: tc.code, Message: tc.message}
		if got := e.IsSiteMismatch(); got != tc.want {
			t.Errorf("IsSiteMismatch(code=%q msg=%q) = %v, want %v", tc.code, tc.message, got, tc.want)
		}
	}
}

func TestNonceIsUUIDv4Shape(t *testing.T) {
	n := randomNonce()
	if len(n) != 36 || n[14] != '4' {
		t.Errorf("nonce %q is not UUIDv4-shaped", n)
	}
}

func TestWithBaseURLOverride(t *testing.T) {
	// 端点覆盖后，请求应直达自定义 base（无需域名改写 transport）
	var gotAction string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAction = parseQueryOf(t, r).Get("Action")
		verifySignature(t, parseQueryOf(t, r))
		_, _ = w.Write([]byte(`{"Code":"Success","Data":{"AvailableAmount":"1","Currency":"CNY"}}`))
	}))
	defer ts.Close()

	c := New("testid", "testsecret", WithBaseURL(ts.URL))
	c.nonce = func() string { return "n-fixed" }
	b, err := c.QueryAccountBalance(context.Background(), SiteChina)
	if err != nil {
		t.Fatalf("QueryAccountBalance: %v", err)
	}
	if b.AvailableAmount != 1 {
		t.Errorf("amount = %v", b.AvailableAmount)
	}
	if gotAction != "QueryAccountBalance" {
		t.Errorf("server saw Action = %q", gotAction)
	}
}
