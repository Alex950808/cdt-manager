// Package alicloud 是一个零依赖的阿里云 OpenAPI 客户端，覆盖 ECS / CDT / BSS
// 三个产品的 6 个 RPC 风格接口。
//
// RPC 签名（SignatureVersion 1.0，HMAC-SHA1）、请求构造、错误解析均为本包
// 独立实现，不依赖任何官方或第三方 SDK。
//
// 覆盖的接口一览：
//
//	产品   版本          Action                   域名                                        用途
//	ECS    2014-05-26   DescribeInstances        ecs.{region}.aliyuncs.com                   实例状态 / 停机模式 / 公网 IP
//	ECS    2014-05-26   StartInstances           同上                                        启动实例
//	ECS    2014-05-26   StopInstances            同上                                        停止实例（节省停机 / 普通停机）
//	CDT    2021-08-13   ListCdtInternetTraffic   cdt.aliyuncs.com（固定 cn-hongkong）         互联网流量明细
//	BSS    2017-12-14   QueryAccountBalance      business.aliyuncs.com（中国站）              账户余额
//	                                          business.ap-southeast-1.aliyuncs.com（国际站）
//	BSS    2017-12-14   DescribeInstanceBill     同上                                        实例月账单明细
//
// 典型用法：
//
//	c := alicloud.New(os.Getenv("ALIYUN_AK_ID"), os.Getenv("ALIYUN_AK_SECRET"))
//	inst, err := c.DescribeInstance(ctx, "cn-hongkong", "i-bp1xxxx")
//	bal, err := c.AccountBalance(ctx, alicloud.SiteChina)   // 带站点自动纠正与缓存
//	gb, err := c.InternetTrafficGB(ctx)                      // 当月出方向流量（GB）
package alicloud
