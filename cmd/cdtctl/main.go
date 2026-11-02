// cdtctl —— 演示 alicloud 包的命令行工具。
//
// 环境变量（命令行 flag 优先）：
//
//	ALIYUN_ACCESS_KEY_ID      AccessKey ID
//	ALIYUN_ACCESS_KEY_SECRET  AccessKey Secret
//	ALIYUN_REGION             实例所在地域（默认 cn-hongkong）
//	ALIYUN_INSTANCE_ID        实例 ID（status/start/stop/bill 需要）
//	ALIYUN_SITE               站点 china | international（默认 china）
//
// 用法：
//
//	cdtctl status                          实例状态 / 公网 IP / 停机模式
//	cdtctl start                           启动实例
//	cdtctl stop [--plain]                  停止实例（默认节省停机，--plain 普通停机）
//	cdtctl traffic                         当月 CDT 互联网流量（GB）
//	cdtctl balance                         账户可用余额
//	cdtctl bill [--cycle 2025-10]          实例当月账单汇总
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"cdt-manager/alicloud"
)

func main() {
	var (
		akP     = flagOrEnv("ak", "ALIYUN_ACCESS_KEY_ID", "")
		skP     = flagOrEnv("sk", "ALIYUN_ACCESS_KEY_SECRET", "")
		regionP = flagOrEnv("region", "ALIYUN_REGION", "cn-hongkong")
		instP   = flagOrEnv("instance", "ALIYUN_INSTANCE_ID", "")
		siteP   = flagOrEnv("site", "ALIYUN_SITE", "china")
		baseP   = flagOrEnv("base-url", "ALIYUN_BASE_URL", "")
		mode    = flag.String("mode", alicloud.StoppedModeStopCharging, "停机模式：StopCharging（节省停机）| Stop（普通停机）")
		plain   = flag.Bool("plain", false, "stop 子命令：使用普通停机（等价 -mode Stop）")
		noFall  = flag.Bool("no-fallback", false, "stop 子命令：节省停机失败时不退化为普通停机")
		cycle   = flag.String("cycle", time.Now().Format("2006-01"), "bill 子命令：账期 YYYY-MM")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "cdtctl — 阿里云 ECS/CDT/BSS 接口演示客户端\n\n用法: cdtctl <status|start|stop|traffic|balance|bill> [flags]\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	ak, sk, region, instance, site, base := *akP, *skP, *regionP, *instP, *siteP, *baseP

	if ak == "" || sk == "" {
		fatal("缺少凭证：请设置 ALIYUN_ACCESS_KEY_ID / ALIYUN_ACCESS_KEY_SECRET（或 --ak/--sk）")
	}

	opts := []alicloud.Option{alicloud.WithBalanceCache(6 * time.Hour)}
	if base != "" { // 指向本地 mock / 自定义网关
		opts = append(opts, alicloud.WithBaseURL(base))
	}
	client := alicloud.New(ak, sk, opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := strings.ToLower(flag.Arg(0))
	switch cmd {
	case "status":
		requireInstance(instance)
		runStatus(ctx, client, region, instance)
	case "start":
		requireInstance(instance)
		runStart(ctx, client, region, instance)
	case "stop":
		requireInstance(instance)
		runStop(ctx, client, region, instance, stopArgs{
			requested: pickStopMode(*mode, *plain),
			fallback:  !*noFall,
		})
	case "traffic":
		runTraffic(ctx, client)
	case "balance":
		runBalance(ctx, client, alicloud.ParseSite(site))
	case "bill":
		requireInstance(instance)
		runBill(ctx, client, alicloud.ParseSite(site), *cycle, instance)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

// --- 子命令 ---------------------------------------------------------------

func runStatus(ctx context.Context, c *alicloud.Client, region, instanceID string) {
	inst, err := c.DescribeInstance(ctx, region, instanceID)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("实例     %s\n状态     %s\n", inst.ID, inst.Status)
	if inst.StoppedMode != "" {
		fmt.Printf("停机模式 %s\n", inst.StoppedMode)
	}
	fmt.Printf("公网 IP  %s\n地域     %s (zone %s)\n", orDash(inst.PublicIP), inst.RegionID, inst.ZoneID)
}

func runStart(ctx context.Context, c *alicloud.Client, region, instanceID string) {
	if err := c.StartInstance(ctx, region, instanceID); err != nil {
		var apiErr *alicloud.APIError
		if errors.As(err, &apiErr) && apiErr.IsInsufficientInventory() {
			fatal("库存不足（InsufficientInventory），该地域暂无可用抢占式库存")
		}
		fatal(err)
	}
	fmt.Printf("✅ 已提交启动请求：%s（状态就绪需要几十秒，可用 status 观察）\n", instanceID)
}

type stopArgs struct {
	requested string
	fallback  bool
}

func runStop(ctx context.Context, c *alicloud.Client, region, instanceID string, args stopArgs) {
	effective, err := c.StopInstance(ctx, region, instanceID, args.requested, args.fallback)
	if err != nil {
		fatal(err)
	}
	if effective == alicloud.StoppedModeStopCharging {
		fmt.Printf("✅ 已提交节省停机：%s（不计实例规格费用）\n", instanceID)
		return
	}
	fmt.Printf("✅ 已提交普通停机：%s（该实例不支持节省停机，仍按规格计费）\n", instanceID)
}

func runTraffic(ctx context.Context, c *alicloud.Client) {
	gb, err := c.InternetTrafficGB(ctx)
	if err != nil {
		fatal(err)
	}
	if gb == nil {
		fmt.Println("⚠️  接口成功但未返回任何流量明细（月初重置或接口抖动，请结合上次读数判断）")
		return
	}
	fmt.Printf("当月 CDT 互联网流量（出方向合计）：%.2f GB\n", *gb)
}

func runBalance(ctx context.Context, c *alicloud.Client, site alicloud.Site) {
	b, err := c.AccountBalance(ctx, site)
	if err != nil {
		fatal(err)
	}
	currency := b.Currency
	if currency == "" {
		currency = "CNY"
	}
	fmt.Printf("可用余额 %.2f %s（站点 %s）\n", b.AvailableAmount, currency, b.Site)
}

func runBill(ctx context.Context, c *alicloud.Client, site alicloud.Site, cycle, instanceID string) {
	s, err := c.InstanceBill(ctx, site, cycle, instanceID)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("%s 账期实例 %s 税前消费：%.2f（%d 条明细，站点 %s）\n", s.Cycle, instanceID, s.Total, s.Details, s.Site)
}

// --- 辅助 -----------------------------------------------------------------

func flagOrEnv(name, env, def string) *string {
	v := os.Getenv(env)
	if v == "" {
		v = def
	}
	return flag.String(name, v, "或环境变量 "+env)
}

func pickStopMode(mode string, plain bool) string {
	if plain {
		return alicloud.StoppedModeStop
	}
	if mode == alicloud.StoppedModeStop || mode == alicloud.StoppedModeStopCharging {
		return mode
	}
	fatal("非法 -mode：%s（可选 StopCharging / Stop）", mode)
	return ""
}

func requireInstance(instance string) {
	if instance == "" {
		fatal("缺少实例 ID：请设置 ALIYUN_INSTANCE_ID（或 --instance）")
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func fatal(format any, args ...any) {
	switch f := format.(type) {
	case string:
		fmt.Fprintf(os.Stderr, "❌ %s\n", fmt.Sprintf(f, args...))
	default:
		fmt.Fprintf(os.Stderr, "❌ %v\n", format)
	}
	os.Exit(1)
}
