package demo

import (
	"fmt"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// 参考演示设备使用的 Capability 引用（capability-model.md §2 的 cloudpath.dev
// 命名空间；均为设备无关的通用能力，不含任何行业/硬件语义）。
// 破坏性语义变化发布 @2，不原地改 @1。
const (
	capCounter     = "cloudpath.dev/capability/counter@1"
	capUptime      = "cloudpath.dev/capability/uptime@1"
	capSetpoint    = "cloudpath.dev/capability/setpoint@1"
	capToggle      = "cloudpath.dev/capability/toggle@1"
	capDiagnostics = "cloudpath.dev/capability/diagnostics@1"
)

// Entity ID（Driver 范围内稳定，重连/重启不变；unique_key 用户不可改）。
const (
	entityHeartbeat   = "heartbeat"
	entityLevel       = "level"
	entitySwitch      = "switch"
	entityUptime      = "uptime"
	entityCommands    = "commands"
	entityDiagnostics = "diagnostics"
)

// setActionSchema 是 set 动作的 inputSchema：前端据此生成参数输入种子
// （{"value":0,"enabled":false}），因此 ParseSetArgs 必须能吃 JSON 对象。
var setActionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		KeyLevel:   map[string]any{"type": "integer", "title": "设定值"},
		KeyEnabled: map[string]any{"type": "boolean", "title": "开关"},
	},
}

// Capabilities 返回参考演示设备使用的 Capability catalog（/api/capabilities 事实源）。
//
// actions 的键**等于命令白名单里的命令名**：前端 commandActions 直接把它渲染成
// 可点击按钮（cmd = action key），因此本设备在 schema-driven UI 里得到的是真实
// 控件，而不是 JSON 编辑器。
func (a *Adapter) Capabilities() []model.Capability {
	return []model.Capability{
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata:   model.CapabilityMetadata{ID: capCounter, Version: 1, Title: "Counter"},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"value": {Type: "number", Access: model.PropertyRead, Quality: []model.Quality{model.QualityGood}},
				},
				Presentation: map[string]any{"primaryProperty": "value", "defaultWidget": "metric"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata:   model.CapabilityMetadata{ID: capUptime, Version: 1, Title: "Uptime"},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"seconds": {Type: "number", Unit: "s", Access: model.PropertyRead, Quality: []model.Quality{model.QualityGood}},
				},
				Presentation: map[string]any{"primaryProperty": "seconds", "defaultWidget": "number"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata:   model.CapabilityMetadata{ID: capSetpoint, Version: 1, Title: "Setpoint"},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"value": {Type: "number", Access: model.PropertyReadWrite, Quality: []model.Quality{model.QualityGood}},
				},
				Actions: map[string]model.ActionDecl{
					CmdSet: {InputSchema: setActionSchema},
				},
				Presentation: map[string]any{"primaryProperty": "value", "defaultWidget": "gauge"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata:   model.CapabilityMetadata{ID: capToggle, Version: 1, Title: "Toggle"},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"state": {Type: "boolean", Access: model.PropertyReadWrite, Quality: []model.Quality{model.QualityGood}},
				},
				Presentation: map[string]any{"primaryProperty": "state", "defaultWidget": "boolean"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata:   model.CapabilityMetadata{ID: capDiagnostics, Version: 1, Title: "Diagnostics"},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"status": {Type: "string", Access: model.PropertyRead, Quality: []model.Quality{model.QualityGood}},
				},
				Events: map[string]model.EventDecl{
					EventBooted: {},
					EventProbed: {},
				},
				Actions: map[string]model.ActionDecl{
					CmdPing: {},
					CmdDump: {},
					CmdNoop: {},
				},
				// headline=true 让设备卡片的主值就是「reference demo device」：
				// 诚实性红线在 UI 首屏即可见，不依赖用户点进详情页。
				Presentation: map[string]any{
					"primaryProperty": "status", "defaultWidget": "badge",
					"headline": true, "tone": "idle",
				},
			},
		},
	}
}

// Descriptor 返回参考演示设备的静态 Descriptor（Entity/Capability 结构 + 身份）。
// 观测值由 dev.Descriptor 在设备实例上填充。
func (a *Adapter) Descriptor(cfg driverkit.Config) model.Descriptor {
	return demoDescriptor(cfg.ID)
}

// demoDescriptor 构造身份 + 实体结构的 Descriptor 骨架（观测值留空）。
func demoDescriptor(id string) model.Descriptor {
	if id == "" {
		id = Name
	}
	return model.Descriptor{
		DeviceID:   id,
		ExternalID: id,
		// 诚实标注：厂商/型号直接说明这是无硬件的参考演示设备。
		Manufacturer: "CloudPath",
		Model:        "Reference Demo Device (no hardware)",
		Status:       model.DeviceUnavailable,
		Entities: []model.Entity{
			{
				EntityID: entityHeartbeat, UniqueKey: entityHeartbeat, Name: "心跳",
				Category: model.EntitySensor, Capabilities: []string{capCounter},
			},
			{
				EntityID: entitySwitch, UniqueKey: entitySwitch, Name: "开关",
				Category: model.EntityActuator, Capabilities: []string{capToggle},
			},
			{
				EntityID: entityUptime, UniqueKey: entityUptime, Name: "运行时长",
				Category: model.EntityDiagnostic, Capabilities: []string{capUptime},
			},
			{
				EntityID: entityCommands, UniqueKey: entityCommands, Name: "命令计数",
				Category: model.EntityDiagnostic, Capabilities: []string{capCounter},
			},
			{
				EntityID: entityDiagnostics, UniqueKey: entityDiagnostics, Name: "诊断",
				Category: model.EntityDiagnostic, Capabilities: []string{capDiagnostics},
			},
			{
				EntityID: entityLevel, UniqueKey: entityLevel, Name: "设定值",
				Category: model.EntityConfig, Capabilities: []string{capSetpoint},
			},
		},
	}
}

// Descriptor 返回带实时观测的完整 Descriptor（Device 级 DescriptorSource 实现）。
//
// 观测值全部来自本进程真实状态（与 Snapshot 同一次 read()，不会互相矛盾）。
//
// observed_at 填**本次采样这些进程内状态的真实时刻**：参考设备的状态是实时可读的，
// 采样即采集，因此既不是零值也不是设备侧不可信的时钟。
// received_at 刻意**不在适配器填**：capability-model.md §4 规定它必须由可信的
// Edge/Core 生成，由 internal/edge 在组装上报信封时统一盖戳。
func (d *dev) Descriptor() model.Descriptor {
	desc := demoDescriptor(d.id)
	v := d.read()
	at := time.Now()

	if v.closed {
		desc.Status = model.DeviceOffline
	} else {
		desc.Status = model.DeviceOnline
	}

	obs := func(capability, property string, value any, unit string) model.Observation {
		return model.Observation{
			Capability: capability, Property: property, Value: value, Unit: unit,
			Quality: model.QualityGood, ObservedAt: at,
		}
	}
	setObservation(&desc, entityHeartbeat, obs(capCounter, "value", v.ticks, ""))
	setObservation(&desc, entityCommands, obs(capCounter, "value", v.commands, ""))
	setObservation(&desc, entityUptime, obs(capUptime, "seconds", v.uptimeS, "s"))
	setObservation(&desc, entityLevel, obs(capSetpoint, "value", v.level, ""))
	setObservation(&desc, entitySwitch, obs(capToggle, "state", v.enabled, ""))
	setObservation(&desc, entityDiagnostics, obs(capDiagnostics, "status", Kind, ""))
	return desc
}

// setObservation 把观测值写入指定 Entity 的 observations（键 = property）。
func setObservation(desc *model.Descriptor, entityID string, obs model.Observation) {
	for i := range desc.Entities {
		if desc.Entities[i].EntityID != entityID {
			continue
		}
		if desc.Entities[i].Observations == nil {
			desc.Entities[i].Observations = map[string]model.Observation{}
		}
		desc.Entities[i].Observations[obs.Property] = obs
		return
	}
	// 结构骨架与观测写入必须同步演化：漏配视为编程错误，早暴露。
	panic(fmt.Sprintf("demo: descriptor 缺少 entity %q", entityID))
}
