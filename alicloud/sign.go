package alicloud

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"slices"
	"strings"
)

// canonicalQuery 把参数表序列化为签名用的规范化查询串：
//
//   - 按参数名做字节序升序排序（Go 字符串比较即字节序）
//   - 名与值分别过 percentEncode
//   - 以 key=value 形式用 & 连接
//
// 该输出同时是最终请求 URL 中查询串的基底（追加 Signature 后即完整查询串），
// 因此两处编码行为天然一致，不会出现「签的是一套、发的是另一套」。
func canonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "Signature" { // 签名本身不参与规范化
			continue
		}
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

// stringToSign 构造待签名字符串：
//
//	HTTPMethod + "&" + percentEncode("/") + "&" + percentEncode(canonicalQuery)
func stringToSign(method, canonicalQuery string) string {
	return method + "&" + percentEncode("/") + "&" + percentEncode(canonicalQuery)
}

// rpcSignature 用 AccessKeySecret 计算 RPC V1 签名：
// base64(HMAC-SHA1(stringToSign, secret + "&"))。
//
// 按协议约定，即使使用 RSA 之外的对称密钥，密钥后也要拼接一个 "&"。
func rpcSignature(accessKeySecret, sts string) string {
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	mac.Write([]byte(sts))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
