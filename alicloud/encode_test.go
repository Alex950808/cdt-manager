package alicloud

import "testing"

func TestPercentEncode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abcABC123", "abcABC123"},
		{"-_.~", "-_.~"},                     // RFC3986 unreserved 全保留（波浪号不编码）
		{"a b", "a%20b"},                     // 空格 → %20（不是 +）
		{"a+b", "a%2Bb"},                     // 加号必须编码
		{"c*d", "c%2Ad"},                     // 星号编码（区别于 net/url）
		{"k=v&k2=v2", "k%3Dv%26k2%3Dv2"},     // 保留字编码
		{"100%", "100%25"},                   // 百分号自身
		{"中文", "%E4%B8%AD%E6%96%87"},         // UTF-8 多字节逐字节大写
		{"[\"i-abc\"]", "%5B%22i-abc%22%5D"}, // 实例 ID 数组的实际形态
		{"08:00:00", "08%3A00%3A00"},         // 时间戳中的冒号
	}
	for _, tc := range cases {
		if got := percentEncode(tc.in); got != tc.want {
			t.Errorf("percentEncode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCanonicalQuerySortsAndEncodes(t *testing.T) {
	// 乱序输入，输出必须按字节序排序
	params := map[string]string{
		"Version":     "2014-05-26",
		"Action":      "DescribeInstances",
		"InstanceIds": `["i-abc"]`,
		"Signature":   "should-be-skipped", // 签名不参与规范化
		"AccessKeyId": "testid",
		"Timestamp":   "2024-06-01T08:00:00Z",
		"Format":      "JSON",
		"Nonce":       "n1",
	}
	want := "AccessKeyId=testid&Action=DescribeInstances&Format=JSON&InstanceIds=%5B%22i-abc%22%5D&Nonce=n1&Timestamp=2024-06-01T08%3A00%3A00Z&Version=2014-05-26"
	if got := canonicalQuery(params); got != want {
		t.Errorf("canonicalQuery:\n got  %s\n want %s", got, want)
	}
}

func TestFinalQueryIncludesSignature(t *testing.T) {
	params := map[string]string{"Action": "Foo", "Signature": "a+b/c"}
	got := finalQuery(params)
	want := "Action=Foo&Signature=a%2Bb%2Fc"
	if got != want {
		t.Errorf("finalQuery = %q, want %q", got, want)
	}
}
