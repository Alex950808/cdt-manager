// Package ddns 是 DNS 同步的执行层：把"某条 A 记录应指向某 IP"落成
// Cloudflare API 调用，并给出人可读的结果状态。
//
// 职责边界：
//
//	本包     只做单条记录的同步执行（校验/短路/重试/状态文案），不认识"绑定"
//	上层     管登记与绑定关系、解析目标 IP、批量巡检与冷却
//	cloudflare 包   纯 API 客户端
package ddns
