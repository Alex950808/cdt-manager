package alicloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternetTrafficGBSumsAndConverts(t *testing.T) {
	// 2GiB + 1GiB = 3GiB；Traffic 字段为字节
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"RequestId":"r-1","TrafficDetails":[
			{"CdtId":"cdt-1","InstanceId":"i-1","ProductType":"ecs","TrafficType":"InternetOutRate","Traffic":1073741824},
			{"CdtId":"cdt-1","InstanceId":"i-1","ProductType":"ecs","TrafficType":"InternetOutRate","Traffic":2147483648},
			{"CdtId":"cdt-1","InstanceId":"i-2","ProductType":"ecs","TrafficType":"InternetOutRate","Traffic":0}
		]}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	gb, err := c.InternetTrafficGB(context.Background())
	if err != nil {
		t.Fatalf("InternetTrafficGB: %v", err)
	}
	if gb == nil || *gb != 3.0 {
		t.Errorf("GB = %v, want 3.0", gb)
	}
}

func TestInternetTrafficGBEmptyDetailsMeansUnknown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"RequestId":"r-1","TrafficDetails":[]}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	gb, err := c.InternetTrafficGB(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gb != nil {
		t.Errorf("empty details should return nil (unknown), got %v", *gb)
	}
}

func TestListCdtInternetTrafficFollowsNextToken(t *testing.T) {
	var tokens []string // 服务端按请求顺序记录收到的 NextToken
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, parseQueryOf(t, r).Get("NextToken"))
		switch len(tokens) {
		case 1:
			_, _ = w.Write([]byte(`{"RequestId":"r-1","NextToken":"page2","TrafficDetails":[{"InstanceId":"i-1","Traffic":100}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"RequestId":"r-2","TrafficDetails":[{"InstanceId":"i-1","Traffic":50}]}`))
		}
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	details, err := c.ListCdtInternetTraffic(context.Background())
	if err != nil {
		t.Fatalf("ListCdtInternetTraffic: %v", err)
	}
	if len(details) != 2 {
		t.Fatalf("details = %d, want 2", len(details))
	}
	if details[0].TrafficBytes != 100 || details[1].TrafficBytes != 50 {
		t.Errorf("traffic bytes wrong: %+v", details)
	}
	if len(tokens) != 2 || tokens[0] != "" || tokens[1] != "page2" {
		t.Errorf("next token sequence = %v", tokens)
	}
	if details[0].InstanceID != "i-1" || details[0].CdtID != "cdt-1" && details[0].CdtID != "" {
		t.Errorf("detail fields not mapped: %+v", details[0])
	}
}

func TestGBConversion(t *testing.T) {
	details := []TrafficDetail{{TrafficBytes: 1 << 30}, {TrafficBytes: 1 << 29}}
	if got := GB(details); got != 1.5 {
		t.Errorf("GB = %v, want 1.5", got)
	}
	if got := GB(nil); got != 0 {
		t.Errorf("GB(nil) = %v, want 0", got)
	}
}
