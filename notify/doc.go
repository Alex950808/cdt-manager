// Package notify 是多渠道通知发送器（零依赖）。
//
// 覆盖渠道与语义：
//
//	Telegram  Bot API sendMessage；HTML 消息，4096 截断，400 时降级纯文本重试，
//	          最多 3 次，支持 HTTP 代理。
//	邮件      SMTP（隐式 SSL 465 / STARTTLS 可选），HTML 正文。
//	Webhook   自定义 URL / 方法 / headers，正文支持 {title} {text} {kind} 模板
//	          （值经 JSON 字符串转义，防自由文本破坏 JSON）。
//
// 原则：任何渠道失败只返回结果不 panic，绝不阻塞主流程（换班/保活）。
package notify
