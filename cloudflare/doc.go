// Package cloudflare 是一个零依赖的 Cloudflare API v4 客户端，
// 覆盖 DNS 记录的查询与更新（DDNS 所需的最小集合）。
//
// 覆盖接口（api.cloudflare.com/client/v4）：
//
//	GET  /zones?name={zone}                          按名称取 Zone ID
//	GET  /zones/{id}/dns_records?type=A&name={name}  查找 DNS 记录（含 Record ID）
//	PUT  /zones/{id}/dns_records/{record}            更新记录内容（A 记录指向新 IP）
//
// 认证使用 API Token（Bearer），需权限 Zone.Read + DNS.Edit。
package cloudflare
