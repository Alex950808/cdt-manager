package main

// 通知桥接：把引擎/服务层的文案构造接到 notify 包（精简版 messages 语义：
// 标题 + 纯文本正文；HTML 版用 <b> 标题 + 逐行正文，Telegram 可读）。

import (
	"context"
	"strings"

	"cdt-manager/notify"
)

// buildMessage 构造双版本消息。
func buildMessage(kind, title, text string) notify.Message {
	var html strings.Builder
	html.WriteString("<b>" + escHTML(title) + "</b>")
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		html.WriteString("<br>" + escHTML(line))
	}
	return notify.Message{Kind: kind, Title: title, HTML: html.String(), Text: text}
}

func escHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func notifySendAll(ctx context.Context, cfg map[string]string, msg notify.Message) map[string]string {
	return notify.SendAll(ctx, notify.Config(cfg), msg)
}

func notifyResultText(results map[string]string, what string) string {
	return notify.ResultText(results, what)
}
