package alicloud

import "strings"

// percentEncode 按阿里云 RPC 签名规范对字符串做百分号编码。
//
// 规则即 RFC 3986 的最严格形式：
//   - 保留字符仅 A-Z a-z 0-9 - _ . ~（波浪号不编码）
//   - 空格编码为 %20（不是表单编码的 +）
//   - 其余每个字节（含多字节 UTF-8 序列的每个字节）编码为 %XX，
//     十六进制必须大写
//
// 不使用 net/url 的 QueryEscape，因为其对空格输出 "+"、无法完全对齐
// 签名所需的字节级行为；这里逐字节实现，行为完全自持。
func percentEncode(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0F])
		}
	}
	return b.String()
}
