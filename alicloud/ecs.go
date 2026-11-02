package alicloud

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- ECS OpenAPI 2014-05-26（RPC 签名，区域化域名） -------------------------
//
// 覆盖：
//	DescribeInstances  查询实例（状态 / 停机模式 / 公网 IP）
//	StartInstances     启动实例
//	StopInstances      停止实例（StoppedMode：StopCharging 节省停机 / KeepCharging 普通停机，强制停止 ForceStop=true）

const (
	ecsVersion  = "2014-05-26"
	ecsActionQ  = "DescribeInstances"
	ecsActionUp = "StartInstances"
	ecsActionDn = "StopInstances"
)

func ecsDomain(regionID string) string { return "ecs." + regionID + ".aliyuncs.com" }

// 停机模式取值。
const (
	StoppedModeStopCharging = "StopCharging" // 节省停机：停机后不计实例规格费用（部分规格/包月不支持）
	StoppedModeKeepCharging = "KeepCharging" // 普通停机（阿里云官方 OpenAPI 参数值）：持续按规格计费
	StoppedModeStop         = "Stop"         // 普通停机兼容常量
)

// Instance 是调用方关心的实例状态子集。
type Instance struct {
	ID          string // i-xxxx
	Status      string // Running / Stopped / Starting / Stopping / Pending
	StoppedMode string // 节省停机时为 StopCharging；运行中为空
	PublicIP    string // EIP 优先，否则经典公网 IP；可能为空
	RegionID    string
	ZoneID      string
}

// ECS 响应中嵌套结构（仅声明用到的字段）。
type ecsInstance struct {
	InstanceID  string `json:"InstanceId"`
	Status      string `json:"Status"`
	StoppedMode string `json:"StoppedMode"`
	RegionID    string `json:"RegionId"`
	ZoneID      string `json:"ZoneId"`
	EipAddress  struct {
		IPAddress string `json:"IpAddress"`
	} `json:"EipAddress"`
	PublicIPAddress struct {
		IPAddress []string `json:"IpAddress"`
	} `json:"PublicIpAddress"`
}

type describeInstancesResponse struct {
	RequestID  string `json:"RequestId"`
	TotalCount int    `json:"TotalCount"`
	Instances  struct {
		Instance []ecsInstance `json:"Instance"`
	} `json:"Instances"`
}

// DescribeInstance 按 ID 查询单台实例的状态。
func (c *Client) DescribeInstance(ctx context.Context, regionID, instanceID string) (*Instance, error) {
	params := map[string]string{
		"Action":      ecsActionQ,
		"Version":     ecsVersion,
		"RegionId":    regionID,
		"InstanceIds": jsonMustArray(instanceID),
	}
	var out describeInstancesResponse
	if err := c.call(ctx, httpPost, ecsDomain(regionID), params, &out); err != nil {
		return nil, fmt.Errorf("ECS DescribeInstances: %w", err)
	}
	if len(out.Instances.Instance) == 0 {
		return nil, fmt.Errorf("ECS DescribeInstances: 实例 %s 不存在或无权访问（region=%s）", instanceID, regionID)
	}
	return newInstance(out.Instances.Instance[0]), nil
}

// newInstance 从原始实例结构提取调用方关心的字段：
// 公网 IP 取 EIP 优先，其次经典公网 IP 列表的首个。
func newInstance(raw ecsInstance) *Instance {
	inst := &Instance{
		ID:          raw.InstanceID,
		Status:      raw.Status,
		StoppedMode: raw.StoppedMode,
		RegionID:    raw.RegionID,
		ZoneID:      raw.ZoneID,
	}
	if raw.EipAddress.IPAddress != "" {
		inst.PublicIP = raw.EipAddress.IPAddress
	} else if len(raw.PublicIPAddress.IPAddress) > 0 {
		inst.PublicIP = raw.PublicIPAddress.IPAddress[0]
	}
	return inst
}

// StartInstance 启动实例。库存不足（抢占式实例常见）返回 *APIError，
// 可用 APIError.IsInsufficientInventory 识别。
func (c *Client) StartInstance(ctx context.Context, regionID, instanceID string) error {
	if instanceID == "" {
		return fmt.Errorf("ECS StartInstances: 实例 ID 不能为空")
	}
	params := map[string]string{
		"Action":       ecsActionUp,
		"Version":      ecsVersion,
		"RegionId":     regionID,
		"InstanceId.1": instanceID,
	}
	if err := c.call(ctx, httpPost, ecsDomain(regionID), params, nil); err != nil {
		return fmt.Errorf("ECS StartInstances: %w", err)
	}
	return nil
}

// StopInstance 停止实例，默认使用强制停止（ForceStop=true）。
//
// stoppedMode 传 StoppedModeStopCharging（节省停机）或 StoppedModeStop / StoppedModeKeepCharging（普通停机）。
// withFallback 为 true 时：节省停机被拒绝（部分规格 / 包年包月不支持）自动退化为
// 普通停机重试，返回值给出**实际生效**的模式，调用方据此渲染文案。
func (c *Client) StopInstance(ctx context.Context, regionID, instanceID, stoppedMode string, withFallback bool) (effectiveMode string, err error) {
	if instanceID == "" {
		return "", fmt.Errorf("ECS StopInstances: 实例 ID 不能为空")
	}
	tryStop := func(mode string) error {
		aliyunMode := mode
		if mode == StoppedModeStop || mode == "Stop" {
			aliyunMode = StoppedModeKeepCharging
		}
		params := map[string]string{
			"Action":       ecsActionDn,
			"Version":      ecsVersion,
			"RegionId":     regionID,
			"InstanceId.1": instanceID,
			"StoppedMode":  aliyunMode,
			"ForceStop":    "true",
		}
		return c.call(ctx, httpPost, ecsDomain(regionID), params, nil)
	}
	if err := tryStop(stoppedMode); err == nil {
		return stoppedMode, nil
	} else if !withFallback || stoppedMode == StoppedModeStop || stoppedMode == StoppedModeKeepCharging {
		return "", fmt.Errorf("ECS StopInstances: %w", err)
	}
	// 退化重试普通停机；仍失败则带出原始错误链
	if err2 := tryStop(StoppedModeKeepCharging); err2 != nil {
		return "", fmt.Errorf("ECS StopInstances（已尝试退化为普通停机）: %w", err2)
	}
	return StoppedModeStop, nil
}

// jsonMustArray 把实例 ID 序列化为协议要求的 JSON 数组字符串（如 ["i-1"]）。
// 输入为普通实例 ID，序列化不会失败，故 panic 仅在编程错误时触发。
func jsonMustArray(instanceID string) string {
	b, err := json.Marshal([]string{instanceID})
	if err != nil {
		panic("alicloud: 实例 ID 序列化失败: " + err.Error())
	}
	return string(b)
}
