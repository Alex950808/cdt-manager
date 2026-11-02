package alicloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueryAccountBalanceParses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Code":"Success","Message":"Successful!","RequestId":"r-1",
			"Data":{"AvailableAmount":"1234.56","AvailableCreditAmount":"0","Currency":"CNY"}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	b, err := c.QueryAccountBalance(context.Background(), SiteChina)
	if err != nil {
		t.Fatalf("QueryAccountBalance: %v", err)
	}
	if b.AvailableAmount != 1234.56 || b.Currency != "CNY" || b.Site != SiteChina {
		t.Errorf("balance = %+v", b)
	}
}

func TestQueryAccountBalanceNumericAmount(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Code":"Success","Data":{"AvailableAmount":88.5,"Currency":"USD"}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	b, _ := c.QueryAccountBalance(context.Background(), SiteInternational)
	if b.AvailableAmount != 88.5 || b.Currency != "USD" {
		t.Errorf("balance = %+v", b)
	}
}

func TestBSSBusinessCodeFailure(t *testing.T) {
	// BSS 特性：业务失败也可能 HTTP 200，靠顶层 Code 判断
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Code":"NotApplicable","Message":"The request site matches no account.","RequestId":"r-2"}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	_, err := c.QueryAccountBalance(context.Background(), SiteChina)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.HTTPStatus != 200 || !apiErr.IsSiteMismatch() {
		t.Errorf("unexpected error: %+v", apiErr)
	}
}

func TestSiteEndpoints(t *testing.T) {
	region, domain := SiteChina.endpoint()
	if region != "cn-hangzhou" || domain != "business.aliyuncs.com" {
		t.Errorf("china endpoint = %s %s", region, domain)
	}
	region, domain = SiteInternational.endpoint()
	if region != "ap-southeast-1" || domain != "business.ap-southeast-1.aliyuncs.com" {
		t.Errorf("intl endpoint = %s %s", region, domain)
	}
	if SiteChina.Opposite() != SiteInternational || SiteInternational.Opposite() != SiteChina {
		t.Error("Opposite broken")
	}
	if ParseSite("international") != SiteInternational || ParseSite("whatever") != SiteChina {
		t.Error("ParseSite broken")
	}
}

func TestAccountBalanceSiteFallback(t *testing.T) {
	var hosts []string // 服务端记录收到的 Host（模拟两个站点域名）
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		if len(hosts) == 1 {
			// 第一跳：站点不匹配
			_, _ = w.Write([]byte(`{"Code":"AuthSiteFail","Message":"The AK is bound to another site."}`))
			return
		}
		_, _ = w.Write([]byte(`{"Code":"Success","Data":{"AvailableAmount":"100.00","Currency":"USD"}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	b, err := c.AccountBalance(context.Background(), SiteChina)
	if err != nil {
		t.Fatalf("AccountBalance: %v", err)
	}
	if b.Site != SiteInternational {
		t.Errorf("effective site = %q, want international", b.Site)
	}
	if b.AvailableAmount != 100 {
		t.Errorf("amount = %v", b.AvailableAmount)
	}
}

func TestAccountBalanceKeepsOriginalErrorWhenFallbackAlsoFails(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"Code":"AuthSiteFail","Message":"no"}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	_, err := c.AccountBalance(context.Background(), SiteInternational)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.IsCode("AuthSiteFail") {
		t.Errorf("should keep original site error, got %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (original + opposite)", calls)
	}
}

func TestInstanceBillSumsItemsAcrossPages(t *testing.T) {
	var cycles []string
	var page atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := parseQueryOf(t, r)
		cycles = append(cycles, q.Get("BillingCycle"))
		if q.Get("NextToken") == "" {
			page.Store(1)
			_, _ = w.Write([]byte(`{"Code":"Success","Data":{"BillingCycle":"2025-10","NextToken":"t2",
				"Items":{"Item":[
					{"InstanceID":"i-1","ProductCode":"ecs","PretaxAmount":"10.25"},
					{"InstanceID":"i-1","ProductCode":"disk","PretaxAmount":"2.00"}
				]}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"Code":"Success","Data":{"BillingCycle":"2025-10",
			"Items":[{"InstanceID":"i-1","ProductCode":"bandwidth","PretaxAmount":0.75}]}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	s, err := c.InstanceBill(context.Background(), SiteChina, "2025-10", "i-1")
	if err != nil {
		t.Fatalf("InstanceBill: %v", err)
	}
	if s.Total != 13.0 || s.Details != 3 || s.Cycle != "2025-10" || s.Site != SiteChina {
		t.Errorf("summary = %+v", s)
	}
	if len(cycles) != 2 || cycles[0] != "2025-10" {
		t.Errorf("billing cycle params = %v", cycles)
	}
}

func TestInstanceBillSiteFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := parseQueryOf(t, r)
		if q.Get("NextToken") != "" {
			_, _ = w.Write([]byte(`{"Code":"Success","Data":{"Items":{"Item":[{"PretaxAmount":"5"}]}}}`))
			return
		}
		if _, domain := SiteChina.endpoint(); r.Host == domain {
			_, _ = w.Write([]byte(`{"Code":"NotApplicable","Message":"site mismatch"}`))
			return
		}
		_, _ = w.Write([]byte(`{"Code":"Success","Data":{"Items":{"Item":[{"PretaxAmount":"7.5"}]}}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	s, err := c.InstanceBill(context.Background(), SiteChina, "2025-10", "i-1")
	if err != nil {
		t.Fatalf("InstanceBill: %v", err)
	}
	if s.Site != SiteInternational || s.Total != 7.5 {
		t.Errorf("summary = %+v, want intl 7.5", s)
	}
}

func TestAccountBalanceCache(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"Code":"Success","Data":{"AvailableAmount":"50.00","Currency":"CNY"}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts, WithBalanceCache(6*time.Hour))
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		b, err := c.AccountBalance(ctx, SiteChina)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if b.AvailableAmount != 50 {
			t.Errorf("call %d amount = %v", i, b.AvailableAmount)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("remote calls = %d, want 1 (cache should absorb repeats)", calls.Load())
	}

	// 强制区分站点：同 AK 不同站点是不同缓存键
	if _, err := c.AccountBalance(ctx, SiteInternational); err != nil {
		t.Fatalf("intl: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("remote calls = %d, want 2 (different site is a different key)", calls.Load())
	}
}

func TestBalanceCacheKeyNotPlaintext(t *testing.T) {
	k := balanceCacheKey("LTAI5tSECRET", SiteChina)
	if len(k) != 20 {
		t.Errorf("key length = %d, want 20", len(k))
	}
	if k == "LTAI5tSECRET" {
		t.Error("cache key must not contain plaintext AK")
	}
	if k == balanceCacheKey("other", SiteChina) {
		t.Error("different AK must map to different key")
	}
}
