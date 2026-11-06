package main

import (
	"strings"
	"testing"
)

func TestBuildMessageTemplates(t *testing.T) {
	tests := []struct {
		kind     string
		title    string
		text     string
		wantIcon string
		wantSub  string
	}{
		{
			kind:     "shift",
			title:    "主力组：换班完成",
			text:     "换班组 主力组 完成切换。\n当班：香港-01",
			wantIcon: "🔄",
			wantSub:  "<code>香港-01</code>",
		},
		{
			kind:     "failover",
			title:    "主力组：故障转移",
			text:     "换班组 主力组 完成切换。\n当班：香港-02\n原因：原当班 香港-01 流量超限",
			wantIcon: "🚨",
			wantSub:  "⚠️ 原当班 香港-01 流量超限",
		},
		{
			kind:     "breaker",
			title:    "主力组：全局熔断",
			text:     "当班流量超限（已超阈值），无可接班的待机成员，已对全组执行节省停机。\n恢复需人工干预：调高阈值或等待流量包重置。",
			wantIcon: "⚡",
			wantSub:  "<blockquote>⚠️ 恢复需人工干预",
		},
		{
			kind:     "limit_stop",
			title:    "香港-01：流量超限已停机",
			text:     "目标实例：香港-01\n流量读数：已用 199.50 GB / 阈值 200 GB",
			wantIcon: "⛔",
			wantSub:  "<code>香港-01</code>",
		},
		{
			kind:     "daily_report",
			title:    "每日流量报告（2026-11-06）",
			text:     "香港-01：运行中，流量 12.3/200GB，余额 50.00 CNY，账单 0.00\n香港-02：已停止，流量 0.0/200GB，余额 50.00 CNY，账单 0.00",
			wantIcon: "📊",
			wantSub:  "🖥 <b>香港-01</b>",
		},
		{
			kind:     "test",
			title:    "测试通知",
			text:     "这是一条来自 CDT 管理台的测试通知。收到即说明渠道配置正确。",
			wantIcon: "🧪",
			wantSub:  "Telegram Bot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			msg := buildMessage(tt.kind, tt.title, tt.text)

			// 绝对不能包含破坏 Telegram HTML 的 <br> 标签
			if strings.Contains(msg.HTML, "<br") {
				t.Errorf("msg.HTML must not contain <br> tags, got: %s", msg.HTML)
			}
			// 必须包含对应图标
			if !strings.Contains(msg.HTML, tt.wantIcon) {
				t.Errorf("msg.HTML want icon %s, got: %s", tt.wantIcon, msg.HTML)
			}
			// 必须包含品牌标识
			if !strings.Contains(msg.HTML, "[CDT]") {
				t.Errorf("msg.HTML want [CDT], got: %s", msg.HTML)
			}
			// 必须包含期望的排版内容
			if !strings.Contains(msg.HTML, tt.wantSub) {
				t.Errorf("msg.HTML want substring %q, got: %s", tt.wantSub, msg.HTML)
			}
			// 必须包含美化分割线
			if !strings.Contains(msg.HTML, "━━━━━━━━━━━━━━━━━━") {
				t.Errorf("msg.HTML want divider, got: %s", msg.HTML)
			}
			// 纯文本版本也必须排版良好
			if !strings.Contains(msg.Text, "------------------------------------") {
				t.Errorf("msg.Text want plain divider, got: %s", msg.Text)
			}
		})
	}
}
