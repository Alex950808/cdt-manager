package main

// 通知桥接：把引擎/服务层的文案构造接到 notify 包。
// 针对 Telegram / 邮件 / Webhook 格式化输出优雅、现代、结构化的排版。

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cdt-manager/notify"
)

func kindIcon(kind string) string {
	switch kind {
	case "shift":
		return "🔄"
	case "failover":
		return "🚨"
	case "breaker":
		return "⚡"
	case "limit_stop", "limitstop":
		return "⛔"
	case "recovery":
		return "✅"
	case "error":
		return "❌"
	case "daily_report", "daily":
		return "📊"
	case "test":
		return "🧪"
	case "keepalive":
		return "💓"
	default:
		return "🔔"
	}
}

func kindTip(kind string) string {
	switch kind {
	case "shift":
		return "💡 <i>DNS 解析已同步平滑切换至新当班实例</i>"
	case "failover":
		return "⚠️ <i>原当班异常，已自动切换备用实例接管并重定向解析</i>"
	case "breaker":
		return "🛑 <i>全组已处于节省停机保护状态，避免产生超额费用</i>"
	case "limit_stop", "limitstop":
		return "💡 <i>已执行节省停机，待机成员不受影响</i>"
	case "recovery":
		return "💡 <i>巡检正常，实例已重新处于监听服务状态</i>"
	case "error":
		return "⚠️ <i>请及时登录控制台或 CDT 管理台检查</i>"
	case "daily_report", "daily":
		return "💡 <i>每日流量与账单汇总，按月重置统计</i>"
	case "test":
		return "💡 <i>后续系统换班、熔断、超限及每日报告将由此渠道推送</i>"
	default:
		return ""
	}
}

// buildMessage 构造美观、结构化的双版本消息（Telegram HTML / 纯文本）。
func buildMessage(kind, title, text string) notify.Message {
	icon := kindIcon(kind)

	// 标题格式化：如 "主力组：换班完成" -> "主力组 · 换班完成"
	displayTitle := title
	if parts := strings.SplitN(title, "：", 2); len(parts) == 2 {
		displayTitle = fmt.Sprintf("%s · %s", parts[0], parts[1])
	} else if parts := strings.SplitN(title, ": ", 2); len(parts) == 2 {
		displayTitle = fmt.Sprintf("%s · %s", parts[0], parts[1])
	}

	var tg strings.Builder
	var plain strings.Builder

	// Header
	tg.WriteString(fmt.Sprintf("%s <b>[CDT] %s</b>\n", icon, escHTML(displayTitle)))
	tg.WriteString("━━━━━━━━━━━━━━━━━━\n")

	plain.WriteString(fmt.Sprintf("[%s] %s\n", icon, displayTitle))
	plain.WriteString("------------------------------------\n")

	// 正文排版
	switch kind {
	case "daily_report", "daily":
		formatDailyReport(&tg, &plain, text)
	case "test":
		formatTestMessage(&tg, &plain, text)
	default:
		formatGeneralMessage(&tg, &plain, text)
	}

	// Footer Tip
	if tip := kindTip(kind); tip != "" {
		tg.WriteString("━━━━━━━━━━━━━━━━━━\n")
		tg.WriteString(tip)
	}

	return notify.Message{
		Kind:  kind,
		Title: title,
		HTML:  strings.TrimRight(tg.String(), "\n"),
		Text:  strings.TrimRight(plain.String(), "\n"),
	}
}

func formatDailyReport(tg, plain *strings.Builder, text string) {
	lines := strings.Split(text, "\n")
	first := true
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !first {
			tg.WriteString("\n")
			plain.WriteString("\n")
		}
		first = false

		parts := strings.SplitN(line, "：", 2)
		if len(parts) != 2 {
			tg.WriteString(escHTML(line) + "\n")
			plain.WriteString(line + "\n")
			continue
		}
		name := strings.TrimSpace(parts[0])
		details := strings.Split(parts[1], "，")

		status := ""
		traffic := ""
		balance := ""
		bill := ""
		for _, d := range details {
			d = strings.TrimSpace(d)
			switch {
			case strings.HasPrefix(d, "流量 "):
				traffic = strings.TrimPrefix(d, "流量 ")
			case strings.HasPrefix(d, "余额 "):
				balance = strings.TrimPrefix(d, "余额 ")
			case strings.HasPrefix(d, "账单 "):
				bill = strings.TrimPrefix(d, "账单 ")
			case status == "":
				status = d
			}
		}

		statusIcon := "🟢"
		switch {
		case strings.Contains(status, "停止"):
			statusIcon = "⚪"
		case strings.Contains(status, "启动"):
			statusIcon = "🟡"
		case strings.Contains(status, "未知"):
			statusIcon = "❓"
		}

		tg.WriteString(fmt.Sprintf("🖥 <b>%s</b>\n", escHTML(name)))
		plain.WriteString(fmt.Sprintf("[%s]\n", name))

		if status != "" {
			tg.WriteString(fmt.Sprintf("  ├ 状态：%s %s\n", statusIcon, escHTML(status)))
			plain.WriteString(fmt.Sprintf("  ├ 状态：%s\n", status))
		}
		if traffic != "" {
			tg.WriteString(fmt.Sprintf("  ├ 流量：<code>%s</code>\n", escHTML(traffic)))
			plain.WriteString(fmt.Sprintf("  ├ 流量：%s\n", traffic))
		}
		if balance != "" {
			tg.WriteString(fmt.Sprintf("  ├ 余额：<code>%s</code>\n", escHTML(balance)))
			plain.WriteString(fmt.Sprintf("  ├ 余额：%s\n", balance))
		}
		if bill != "" {
			tg.WriteString(fmt.Sprintf("  └ 账单：<code>%s</code>\n", escHTML(bill)))
			plain.WriteString(fmt.Sprintf("  └ 账单：%s\n", bill))
		}
	}
}

func formatTestMessage(tg, plain *strings.Builder, text string) {
	tg.WriteString("这是一条来自 CDT 管理台的测试通知。\n\n")
	tg.WriteString("• <b>渠道类型：</b> <code>Telegram Bot</code>\n")
	tg.WriteString("• <b>测试结果：</b> ✅ 连通成功\n")
	nowStr := time.Now().Format("2006-01-02 15:04:05")
	tg.WriteString(fmt.Sprintf("• <b>发送时间：</b> <code>%s</code>\n", nowStr))

	plain.WriteString("这是一条来自 CDT 管理台的测试通知。\n\n")
	plain.WriteString("• 渠道类型: Telegram Bot\n")
	plain.WriteString("• 测试结果: 通信正常\n")
	plain.WriteString(fmt.Sprintf("• 发送时间: %s\n", nowStr))
}

func formatGeneralMessage(tg, plain *strings.Builder, text string) {
	lines := strings.Split(text, "\n")
	hasKV := false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		// 告警提示/人工干预提示，使用 Telegram <blockquote> 优雅展示
		if strings.HasPrefix(line, "恢复需人工干预") || strings.HasPrefix(line, "请检查") || strings.Contains(line, "仍未就绪") {
			tg.WriteString(fmt.Sprintf("<blockquote>⚠️ %s</blockquote>\n", escHTML(line)))
			plain.WriteString(fmt.Sprintf("[提示] %s\n", line))
			continue
		}

		// 换班组切换摘要句：如 "换班组 主力组 完成切换。"
		if strings.HasPrefix(line, "换班组 ") && strings.HasSuffix(line, " 完成切换。") {
			group := strings.TrimSuffix(strings.TrimPrefix(line, "换班组 "), " 完成切换。")
			tg.WriteString(fmt.Sprintf("换班组 <b>%s</b> 已完成切换。\n\n", escHTML(group)))
			plain.WriteString(fmt.Sprintf("换班组 %s 已完成切换。\n\n", group))
			continue
		}

		// 检查冒号键值对：如 "当班：香港-01" 或 "原因：原当班 香港-02 流量超限"
		sep := "："
		idx := strings.Index(line, "：")
		if idx == -1 {
			sep = ":"
			idx = strings.Index(line, ":")
		}

		if idx != -1 {
			hasKV = true
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+len(sep):])
			valHTML := escHTML(val)

			if key == "当班" || key == "原当班" || key == "接替当班" || key == "目标实例" {
				valHTML = fmt.Sprintf("<code>%s</code>", valHTML)
			} else if strings.Contains(key, "原因") {
				valHTML = fmt.Sprintf("⚠️ %s", valHTML)
			}

			tg.WriteString(fmt.Sprintf("• <b>%s：</b> %s\n", escHTML(key), valHTML))
			plain.WriteString(fmt.Sprintf("• %s: %s\n", key, val))
		} else {
			tg.WriteString(fmt.Sprintf("%s\n", escHTML(line)))
			plain.WriteString(fmt.Sprintf("%s\n", line))
		}
	}

	// 补充时间戳
	if hasKV {
		nowStr := time.Now().Format("2006-01-02 15:04:05")
		tg.WriteString(fmt.Sprintf("• <b>发生时间：</b> <code>%s</code>\n", nowStr))
		plain.WriteString(fmt.Sprintf("• 发生时间: %s\n", nowStr))
	}
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
