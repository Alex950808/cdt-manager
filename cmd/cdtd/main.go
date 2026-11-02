// cdtd —— CDT 管理后台：多实例管理 + 仪表盘 + 云操作 + 保活/换班引擎。
//
// 在 alicloud 接口层之上的应用服务：
//
//	Web 界面（go:embed 单页）+ REST API + JSON 文件存储（零数据库依赖）
//	功能：实例 CRUD、状态/流量/余额/账单刷新、启动/停止（节省停机自动降级）、
//	     单机保活、换班组（流量超限转移 / 起不来转移 / 熔断 / 限额停机）、
//	     Cloudflare DDNS、通知（Telegram / 邮件 / Webhook）、调度器、事件流。
//
// 连真实阿里云：
//
//	cdtd -data cdtd-data.json -token 你的口令
//	（实例的 AK 在界面里添加；不要设置 -base）
//
// 连本地 mock 演示：
//
//	mockaliyun -addr 127.0.0.1:8123 -quirks &
//	cdtd -base http://127.0.0.1:8123 -seed-mock
//	打开 http://127.0.0.1:8080
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	var (
		addr     = flag.String("addr", "127.0.0.1:8080", "监听地址")
		dataPath = flag.String("data", "cdtd-data.json", "数据文件路径（JSON 或 SQLite .db/.sqlite）")
		driver   = flag.String("driver", "auto", "存储引擎：auto | json | sqlite（auto 模式下 .db/.sqlite 文件自动走 sqlite）")
		base     = flag.String("base", os.Getenv("ALIYUN_BASE_URL"), "覆盖阿里云端点（本地 mock / 网关）；留空连真实阿里云")
		token    = flag.String("token", os.Getenv("CDTD_TOKEN"), "API 鉴权 Token（留空不启用）")
		seedMock = flag.Bool("seed-mock", false, "首次启动时预置两台 mock 演示实例")
		noSched  = flag.Bool("no-sched", false, "不启动保活/换班调度器（仅 UI 调试用）")
	)
	flag.Parse()

	masterKey := filepath.Join(filepath.Dir(*dataPath), "master.key")
	store, err := NewStoreWithDriver(*dataPath, masterKey, *driver)
	if err != nil {
		log.Fatalf("读取数据文件 %s 失败: %v", *dataPath, err)
	}
	defer store.Close()
	srv := NewServer(store, *base, *token)

	// 无人值守初始化：提供环境变量则跳过向导直接创建管理员
	if _, has := store.AdminGet(); !has {
		bu, bp := os.Getenv("CDTD_ADMIN_USERNAME"), os.Getenv("CDTD_ADMIN_PASSWORD")
		if bu != "" && bp != "" {
			if hash, err := HashPassword(bp); err == nil {
				if err := store.AdminSet(AdminAccount{Username: bu, PassHash: hash}); err == nil {
					log.Printf("已通过环境变量创建管理员：%s", bu)
				}
			}
		}
	}

	if *seedMock && len(store.List()) == 0 {
		for _, in := range []*Instance{
			{Name: "香港-01", AccessKeyID: "mock-ak", AccessKeySecret: "mock-secret",
				RegionID: "cn-hongkong", InstanceID: "i-mock001", Site: "china", Note: "演示实例"},
			{Name: "香港-02", AccessKeyID: "mock-ak", AccessKeySecret: "mock-secret",
				RegionID: "cn-hongkong", InstanceID: "i-mock002", Site: "china", Note: "演示实例"},
		} {
			if _, err := store.Create(in); err != nil {
				log.Fatalf("预置演示实例失败: %v", err)
			}
		}
		log.Printf("已预置 2 台演示实例（mock）")
	}

	if !*noSched {
		srv.StartScheduler()
		log.Printf("保活/换班调度器已启动")
	}

	handler := srv.withAuth(srv.Routes())
	log.Printf("cdtd 管理后台启动：http://%s（数据文件 %s）", *addr, *dataPath)
	if *base != "" {
		log.Printf("阿里云端点已覆盖：%s", *base)
	}
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}
