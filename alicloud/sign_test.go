package alicloud

import "testing"

// 期望值由独立的 Node.js crypto 脚本按协议手工计算生成（见仓库 README），
// 不是本包函数的输出，避免实现自证。
func TestSignatureAgainstReferenceVectors(t *testing.T) {
	t.Run("RPC POST with InstanceIds array", func(t *testing.T) {
		params := map[string]string{
			"AccessKeyId":      "testid",
			"Action":           "DescribeInstances",
			"Format":           "JSON",
			"InstanceIds":      `["i-abc"]`,
			"SignatureMethod":  "HMAC-SHA1",
			"SignatureNonce":   "abc123",
			"SignatureVersion": "1.0",
			"Timestamp":        "2024-06-01T08:00:00Z",
			"Version":          "2014-05-26",
		}
		sts := stringToSign("POST", canonicalQuery(params))
		wantSTS := "POST&%2F&AccessKeyId%3Dtestid%26Action%3DDescribeInstances%26Format%3DJSON%26InstanceIds%3D%255B%2522i-abc%2522%255D%26SignatureMethod%3DHMAC-SHA1%26SignatureNonce%3Dabc123%26SignatureVersion%3D1.0%26Timestamp%3D2024-06-01T08%253A00%253A00Z%26Version%3D2014-05-26"
		if sts != wantSTS {
			t.Fatalf("stringToSign:\n got  %s\n want %s", sts, wantSTS)
		}
		if got := rpcSignature("testsecret", sts); got != "PuMTgaMBw7+9HQCc2tGCsRennUs=" {
			t.Errorf("rpcSignature = %q, want PuMTgaMBw7+9HQCc2tGCsRennUs=", got)
		}
	})

	t.Run("GET with special chars and multibyte", func(t *testing.T) {
		params := map[string]string{
			"AccessKeyId":      "ak",
			"Action":           "Foo.Bar",
			"Format":           "JSON",
			"Note":             "a b+c*d~e/f",
			"Tag":              "中文值",
			"SignatureMethod":  "HMAC-SHA1",
			"SignatureNonce":   "n-2",
			"SignatureVersion": "1.0",
			"Timestamp":        "2025-01-02T03:04:05Z",
			"Version":          "2017-12-14",
		}
		sts := stringToSign("GET", canonicalQuery(params))
		if got := rpcSignature("secret2", sts); got != "SX5Y75IIfAc0wEaWOMi2OKb9/+A=" {
			t.Errorf("rpcSignature = %q, want SX5Y75IIfAc0wEaWOMi2OKb9/+A=", got)
		}
	})
}

func TestStringToSignShape(t *testing.T) {
	// POST&%2F&<encoded>：斜杠和空段都必须编码
	got := stringToSign("POST", "A=B")
	want := "POST&%2F&A%3DB"
	if got != want {
		t.Errorf("stringToSign = %q, want %q", got, want)
	}
}
