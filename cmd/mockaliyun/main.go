// mockaliyun —— 本地模拟阿里云 RPC 端点，用于无凭证演示与联调。
//
// 关键点：本文件的签名校验是**独立于 alicloud 包的第二份实现**
// （自行编解码、自行 HMAC），从服务端反向验证客户端发出的请求
// 签名合法 —— 与单元测试的金标准向量构成双重交叉验证。
//
// 支持多实例：DescribeInstances / StartInstances / StopInstances 按请求中的
// InstanceIds 动态回显，每台实例独立状态机；流量与账单按实例 ID 哈希出
// 稳定数值，保证管理后台多实例演示时数据可复现。
//
// 用法：
//
//	mockaliyun -addr 127.0.0.1:8123 [-secret mock-secret] [-quirks]
//
// -quirks 打开一组"故意捣乱"的场景，演示客户端的健壮性：
//   - 中国站 BSS 请求返回 AuthSiteFail（客户端应自动换国际站成功）
//   - StopCharging 节省停机返回不支持（客户端应降级为普通停机）
//   - 账单接口返回 NextToken 分页（客户端应自动翻页取全）
package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8123", "监听地址")
	secret := flag.String("secret", "mock-secret", "期望的 AccessKeySecret（校验签名用）")
	quirks := flag.Bool("quirks", false, "开启故障注入：站点错误 / 停机降级 / 账单分页")
	flag.Parse()

	srv := newMockServer(*secret, *quirks)

	log.Printf("mock-aliyun listening on http://%s (secret=%q quirks=%v)", *addr, *secret, *quirks)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatal(err)
	}
}

type mockServer struct {
	secret string
	quirks bool

	mu      sync.Mutex
	counts  map[string]int // 各 Action 调用计数（控制台展示）
	started time.Time
	ec2     map[string]*ec2State // 按实例 ID 独立状态机
}

// ec2State 是单台模拟实例的状态。
type ec2State struct {
	status string // Running / Starting / Stopping / Stopped
	mode   string
}

func newMockServer(secret string, quirks bool) *mockServer {
	return &mockServer{
		secret:  secret,
		quirks:  quirks,
		counts:  map[string]int{},
		started: time.Now(),
		ec2:     map[string]*ec2State{"i-mock001": {status: "Running"}},
	}
}

func (s *mockServer) bump(action string) {
	s.mu.Lock()
	s.counts[action]++
	s.mu.Unlock()
}

// instance 取（或惰性创建）一台实例的状态机。
func (s *mockServer) instance(id string) *ec2State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.ec2[id]
	if !ok {
		st = &ec2State{status: "Running"}
		s.ec2[id] = st
	}
	return st
}

// transition 切换到过渡态并在 delay 后到达最终态（模拟云操作异步到位）。
func (s *mockServer) transition(id, interim, final, mode string, delay time.Duration) {
	st := s.instance(id)
	s.mu.Lock()
	st.status, st.mode = interim, mode
	s.mu.Unlock()
	time.AfterFunc(delay, func() {
		s.mu.Lock()
		st.status = final
		st.mode = ""
		if final == "Stopped" {
			st.mode = mode
		}
		s.mu.Unlock()
	})
}

// snapshot 拷贝全部实例状态（控制台渲染用）。
func (s *mockServer) snapshot() map[string]ec2State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ec2State, len(s.ec2))
	for id, st := range s.ec2 {
		status := st.status
		if status == "" {
			status = "Running"
		}
		out[id] = ec2State{status: status, mode: st.mode}
	}
	return out
}

// --- 服务端独立签名校验（第二份实现，勿与 alicloud 包共用代码） -------------

func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0F])
		}
	}
	return b.String()
}

func verifySignature(method string, q url.Values, secret string) bool {
	got := q.Get("Signature")
	if got == "" {
		return false
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		if k == "Signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canon strings.Builder
	for i, k := range keys {
		if i > 0 {
			canon.WriteByte('&')
		}
		canon.WriteString(percentEncode(k))
		canon.WriteByte('=')
		canon.WriteString(percentEncode(q.Get(k)))
	}
	sts := method + "&" + percentEncode("/") + "&" + percentEncode(canon.String())
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(sts))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)) == got
}

// hashSeed 把实例 ID 散列为稳定正整数，用于生成可复现的 IP / 流量 / 账单。
func hashSeed(id string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return h.Sum32()
}

// mockIP 按 ID 生成稳定的仿真公网 IP（47.243.x.x 为阿里云香港常见段）。
func mockIP(id string) string {
	seed := hashSeed(id)
	return fmt.Sprintf("47.243.%d.%d", seed%200+10, (seed>>8)%240+20)
}

// mockTrafficGB 按实例 ID 生成 20~150 GB 的稳定流量。
func mockTrafficGB(id string) float64 {
	return float64(hashSeed(id)%13000)/100 + 20
}

// --- HTTP 处理 --------------------------------------------------------------

func fail(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"Code": code, "Message": message, "RequestId": "mock-" + fmt.Sprint(time.Now().UnixNano()),
	})
}

func ok(w http.ResponseWriter, body map[string]any) {
	body["RequestId"] = "mock-" + fmt.Sprint(time.Now().UnixNano())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (s *mockServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	action := q.Get("Action")

	// 浏览器直接打开（无 Action / 无签名）：返回控制台页而非裸错误
	if action == "" {
		s.serveConsole(w)
		return
	}

	if !verifySignature(r.Method, q, s.secret) {
		log.Printf("[%-28s] ✗ 签名校验失败（secret 期望 %q）", action, s.secret)
		s.bump("✗ 拒绝（签名不合法）")
		fail(w, "SignatureDoesNotMatch", "server-side recompute mismatch")
		return
	}
	log.Printf("[%-28s] ✓ 签名校验通过 ak=%s nonce=%s", action, q.Get("AccessKeyId"), q.Get("SignatureNonce"))
	s.bump(action)

	switch action {
	case "DescribeInstances":
		s.handleDescribe(w, extractInstanceIDs(q))
	case "StartInstances", "StartInstance":
		s.handleStart(w, extractInstanceIDs(q))
	case "StopInstances", "StopInstance":
		mode := q.Get("StoppedMode")
		if mode == "KeepCharging" {
			mode = "Stop"
		}
		s.handleStop(w, extractInstanceIDs(q), mode)
	case "ListCdtInternetTraffic":
		s.handleTraffic(w, q.Get("NextToken"))
	case "QueryAccountBalance":
		s.handleBalance(w, r.URL.Path) // WithBaseURL 模式下产品域名在路径里
	case "DescribeInstanceBill":
		s.handleBill(w, q.Get("InstanceID"), q.Get("NextToken"))
	default:
		fail(w, "InvalidAction", "unknown action: "+action)
	}
}

// extractInstanceIDs 解析请求中的实例 ID 列表，支持 JSON 数组 InstanceIds、
// 批量序列 InstanceId.N 以及单个 InstanceId 参数。
func extractInstanceIDs(q url.Values) []string {
	var ids []string
	if raw := q.Get("InstanceIds"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &ids)
	}
	if len(ids) == 0 {
		for i := 1; i <= 100; i++ {
			if id := q.Get(fmt.Sprintf("InstanceId.%d", i)); id != "" {
				ids = append(ids, id)
			} else {
				break
			}
		}
	}
	if len(ids) == 0 && q.Get("InstanceId") != "" {
		ids = []string{q.Get("InstanceId")}
	}
	if len(ids) == 0 {
		ids = []string{"i-mock001"}
	}
	return ids
}

// --- 控制台页（浏览器直接访问时展示） ---------------------------------------

func (s *mockServer) serveConsole(w http.ResponseWriter) {
	states := s.snapshot()
	s.mu.Lock()
	counts := make(map[string]int, len(s.counts))
	for k, v := range s.counts {
		counts[k] = v
	}
	s.mu.Unlock()

	rejected := counts["✗ 拒绝（签名不合法）"]
	upFor := time.Since(s.started).Round(time.Second)

	var instRows strings.Builder
	for id, st := range states {
		color := map[string]string{
			"Running": "#16a34a", "Starting": "#d97706", "Stopping": "#d97706", "Stopped": "#6b7280",
		}[st.status]
		fmt.Fprintf(&instRows, `<tr><td class="mono">%s</td><td class="mono" style="color:%s">%s</td><td class="mono">%s</td><td>%s</td></tr>`,
			id, color, st.status, mockIP(id), orEmpty(st.mode))
	}

	var actionRows strings.Builder
	for _, a := range []string{
		"DescribeInstances", "StartInstances", "StopInstances",
		"ListCdtInternetTraffic", "QueryAccountBalance", "DescribeInstanceBill",
	} {
		fmt.Fprintf(&actionRows, `<tr><td class="mono">%s</td><td>%d</td></tr>`, a, counts[a])
	}

	quirkNote := "关闭（所有请求直接成功，节省停机全部支持）"
	if s.quirks {
		quirkNote = "开启：中国站余额→AuthSiteFail｜节省停机→部分实例拒绝（按 ID 哈希）自动降级｜账单/流量→NextToken 分页"
	}

	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh"><head><meta charset="utf-8">
<meta http-equiv="refresh" content="5">
<title>mock-aliyun 控制台</title>
<style>
 body{font-family:"Segoe UI",system-ui,sans-serif;background:#f6f7fb;color:#1f2937;margin:0;padding:32px}
 .wrap{max-width:820px;margin:0 auto}
 h1{font-size:22px;margin:0 0 4px} .sub{color:#6b7280;font-size:13px;margin-bottom:24px}
 .card{background:#fff;border:1px solid #e5e7eb;border-radius:12px;padding:18px 22px;margin-bottom:16px}
 .card h2{font-size:14px;margin:0 0 12px;color:#374151}
 table{border-collapse:collapse;width:100%%;font-size:13px}
 td{padding:5px 8px;border-bottom:1px solid #f1f2f6}
 td:last-child{text-align:right;color:#6b7280}
 .mono{font-family:Consolas,monospace}
 .kv{display:flex;gap:12px;font-size:13px;margin:4px 0}
</style></head><body><div class="wrap">
<h1>mock-aliyun · 本地模拟阿里云端点</h1>
<div class="sub">运行 %s ｜ 已处理 %d 个请求（拒绝 %d）｜ 页面每 5 秒自动刷新</div>

<div class="card">
 <h2>模拟实例（共 %d 台，状态实时）</h2>
 <table>%s</table>
</div>

<div class="card">
 <h2>接口调用计数</h2>
 <table>%s</table>
</div>

<div class="card">
 <h2>故障注入（-quirks）</h2>
 <div style="font-size:13px">%s</div>
</div>

<div class="card">
 <h2>用法</h2>
 <div style="font-size:13px;line-height:1.9">
  管理后台：<code>cdtd -base http://127.0.0.1:8123</code> → 打开 <code>http://127.0.0.1:8080</code><br>
  命令行：<code>ALIYUN_BASE_URL=http://127.0.0.1:8123 cdtctl status</code>
 </div>
 <p style="font-size:12px;color:#6b7280">浏览器直接访问没有签名，不会进入接口逻辑；签名校验由服务端独立重算完成。</p>
</div>
</div></body></html>`,
		upFor, totalOf(counts)+rejected, rejected,
		len(states), instRows.String(), actionRows.String(), quirkNote)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func totalOf(counts map[string]int) int {
	n := 0
	for _, v := range counts {
		n += v
	}
	return n
}

func orEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// --- 各 Action --------------------------------------------------------------

func (s *mockServer) handleDescribe(w http.ResponseWriter, ids []string) {
	instances := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		st := s.instance(id)
		s.mu.Lock()
		status, mode := st.status, st.mode
		s.mu.Unlock()
		if status == "" {
			status = "Running"
		}
		instances = append(instances, map[string]any{
			"InstanceId":      id,
			"Status":          status,
			"StoppedMode":     mode,
			"RegionId":        "cn-hongkong",
			"ZoneId":          "cn-hongkong-a",
			"EipAddress":      map[string]any{"IpAddress": mockIP(id)},
			"PublicIpAddress": map[string]any{"IpAddress": []string{}},
		})
	}
	ok(w, map[string]any{"TotalCount": len(instances), "Instances": map[string]any{"Instance": instances}})
}

func (s *mockServer) handleStart(w http.ResponseWriter, ids []string) {
	for _, id := range ids {
		s.transition(id, "Starting", "Running", "", 3*time.Second)
	}
	ok(w, map[string]any{})
}

func (s *mockServer) handleStop(w http.ResponseWriter, ids []string, requested string) {
	if requested == "" {
		requested = "Stop"
	}
	// quirks 注入：部分实例不支持节省停机（模拟真实世界不同规格/计费方式的差异，
	// 按实例 ID 哈希区分，保证同一实例行为稳定可复现）。
	// 客户端应自动降级为普通停机；支持的实例则正常节省停机。
	for _, id := range ids {
		if s.quirks && requested == "StopCharging" && hashSeed(id)%2 == 0 {
			fail(w, "InvalidOperation.NotSupportedStoppedMode",
				"instance "+id+" does not support StopCharging")
			return
		}
		s.transition(id, "Stopping", "Stopped", requested, 3*time.Second)
	}
	ok(w, map[string]any{})
}

func (s *mockServer) handleTraffic(w http.ResponseWriter, nextToken string) {
	// 账号级明细：为每台已知实例生成一份稳定流量
	states := s.snapshot()
	details := make([]map[string]any, 0, len(states))
	for id := range states {
		gb := mockTrafficGB(id)
		details = append(details, map[string]any{
			"CdtId": "cdt-mock", "InstanceId": id, "ProductType": "ecs",
			"TrafficType": "InternetOutRate", "Traffic": gb * (1 << 30),
		})
	}
	body := map[string]any{"TrafficDetails": details}
	if s.quirks && nextToken == "" {
		body["NextToken"] = "page-2"
	}
	if nextToken != "" {
		body = map[string]any{"TrafficDetails": []map[string]any{
			{"CdtId": "cdt-mock", "InstanceId": "i-shared", "ProductType": "slb",
				"TrafficType": "InternetOutRate", "Traffic": 3.5 * (1 << 30)},
		}}
	}
	ok(w, body)
}

func (s *mockServer) handleBalance(w http.ResponseWriter, endpointPath string) {
	// quirks：中国站端点直接打回站点错误，演示客户端自动换站
	// （"business.ap-southeast-1.aliyuncs.com" 不含 "business.aliyuncs.com" 子串，可安全区分）
	if s.quirks && strings.Contains(endpointPath, "business.aliyuncs.com") {
		fail(w, "AuthSiteFail", "The AK is bound to another site.")
		return
	}
	currency := "CNY"
	if strings.Contains(endpointPath, "ap-southeast-1") {
		currency = "USD"
	}
	ok(w, map[string]any{"Code": "Success", "Data": map[string]any{
		"AvailableAmount": "1286.40", "Currency": currency,
	}})
}

func (s *mockServer) handleBill(w http.ResponseWriter, instanceID, nextToken string) {
	if instanceID == "" {
		instanceID = "i-mock001"
	}
	// 按实例 ID 生成稳定金额：基础 21.58 + 哈希偏移（0~30）
	base := 21.58 + float64(hashSeed(instanceID)%3000)/100
	if nextToken == "" {
		body := map[string]any{
			"Code": "Success",
			"Data": map[string]any{
				"BillingCycle": "2025-10",
				"Items": map[string]any{"Item": []map[string]any{
					{"InstanceID": instanceID, "ProductCode": "ecs", "ProductType": "instance", "SubscriptionType": "PayAsYouGo", "PretaxAmount": fmt.Sprintf("%.2f", base*0.68)},
					{"InstanceID": instanceID, "ProductCode": "ecs", "ProductType": "disk", "SubscriptionType": "PayAsYouGo", "PretaxAmount": fmt.Sprintf("%.2f", base*0.22)},
					{"InstanceID": instanceID, "ProductCode": "ecs", "ProductType": "bandwidth", "SubscriptionType": "PayAsYouGo", "PretaxAmount": fmt.Sprintf("%.2f", base*0.10)},
				}},
			},
		}
		if s.quirks {
			body["Data"].(map[string]any)["NextToken"] = "bill-2"
		}
		ok(w, body)
		return
	}
	ok(w, map[string]any{"Code": "Success", "Data": map[string]any{
		"BillingCycle": "2025-10",
		"Items": []map[string]any{
			{"InstanceID": instanceID, "ProductCode": "slb", "PretaxAmount": 5.00},
		},
	}})
}
