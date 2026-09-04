package stcb

import (
	"fmt"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// STC-B 使用的 Capability 引用（capability-model.md 的 cloudpath.dev 命名空间）。
// 破坏性语义变化发布 @2，不原地改 @1。
const (
	capClock   = "cloudpath.dev/capability/clock@1"
	capAlarm   = "cloudpath.dev/capability/alarm@1"
	capContact = "cloudpath.dev/capability/contact@1"

	capTemp    = "cloudpath.dev/capability/temperature@1"
	capIllum   = "cloudpath.dev/capability/illuminance@1"
	capHall    = "cloudpath.dev/capability/hall@1"
	capVib     = "cloudpath.dev/capability/vibration@1"
	capKey     = "cloudpath.dev/capability/key@1"
	capBuzzer  = "cloudpath.dev/capability/buzzer@1"
	capLED     = "cloudpath.dev/capability/led@1"
	capDisplay = "cloudpath.dev/capability/display-text@1"
	capMotor   = "cloudpath.dev/capability/motor@1"
)

// 执行器命令 key（Capability action 键 == 命令白名单命令名，schema-driven UI 据此渲染成真实控件）。
const (
	cmdBuzzer  = "buzzer"
	cmdLED     = "led"
	cmdDisplay = "display"
	cmdMotor   = "motor"
)

// 执行器 action 的 inputSchema：前端据此生成参数输入种子（占位符 = 声明产生的参数模板）。
var buzzerActionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"freq":     map[string]any{"type": "integer", "minimum": 0, "maximum": 9, "title": "频率档"},
		"duration": map[string]any{"type": "integer", "minimum": 0, "maximum": 9, "title": "时长档"},
	},
	"required": []any{"freq", "duration"},
}

var ledActionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"pattern": map[string]any{"type": "integer", "minimum": 0, "maximum": 9, "title": "LED 档（0=灭 9=全亮）"},
	},
	"required": []any{"pattern"},
}

var displayActionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"digits": map[string]any{
			"type": "array", "items": map[string]any{"type": "integer", "minimum": 0, "maximum": 9},
			"minItems": 8, "maxItems": 8, "title": "8 位数字（0-9）",
		},
	},
	"required": []any{"digits"},
}

var motorActionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"steps": map[string]any{"type": "integer", "minimum": 0, "maximum": 4, "title": "步数档（0=停）"},
	},
	"required": []any{"steps"},
}

// Capabilities 返回 STC-B 使用的 Capability catalog（/api/capabilities 事实源）。
// 设备缺陷/慢发/乱码容错属于 Driver 实现细节，不泄漏进 Capability 语义。
func (a *Adapter) Capabilities() []model.Capability {
	return []model.Capability{
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capClock,
				Version: 1,
				Title:   "Clock",
			},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"time": {Type: "string", Access: model.PropertyRead},
				},
				Presentation: map[string]any{"primaryProperty": "time", "defaultWidget": "text"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capAlarm,
				Version: 1,
				Title:   "Alarm",
			},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"state": {Type: "string", Access: model.PropertyRead},
				},
				Presentation: map[string]any{"primaryProperty": "state", "defaultWidget": "badge"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capContact,
				Version: 1,
				Title:   "Contact",
			},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"state": {Type: "string", Access: model.PropertyRead},
				},
				Presentation: map[string]any{"primaryProperty": "state", "defaultWidget": "badge"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capTemp,
				Version: 1,
				Title:   "Temperature",
			},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"value": {Type: "number", Unit: "Cel", Access: model.PropertyRead, Quality: []model.Quality{model.QualityGood}},
				},
				Presentation: map[string]any{"primaryProperty": "value", "defaultWidget": "gauge"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capIllum,
				Version: 1,
				Title:   "Illuminance",
			},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"value": {Type: "number", Access: model.PropertyRead, Quality: []model.Quality{model.QualityGood}},
				},
				Presentation: map[string]any{"primaryProperty": "value", "defaultWidget": "number"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capHall,
				Version: 1,
				Title:   "Hall",
			},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"state": {Type: "integer", Access: model.PropertyRead, Quality: []model.Quality{model.QualityGood}},
				},
				Presentation: map[string]any{"primaryProperty": "state", "defaultWidget": "badge"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capVib,
				Version: 1,
				Title:   "Vibration",
			},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"state": {Type: "integer", Access: model.PropertyRead, Quality: []model.Quality{model.QualityGood}},
				},
				Presentation: map[string]any{"primaryProperty": "state", "defaultWidget": "badge"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capKey,
				Version: 1,
				Title:   "Key",
			},
			Spec: model.CapabilitySpec{
				Properties: map[string]model.Property{
					"state": {Type: "integer", Access: model.PropertyRead, Quality: []model.Quality{model.QualityGood}},
				},
				Presentation: map[string]any{"primaryProperty": "state", "defaultWidget": "badge"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capBuzzer,
				Version: 1,
				Title:   "Buzzer",
			},
			Spec: model.CapabilitySpec{
				Actions: map[string]model.ActionDecl{
					cmdBuzzer: {Title: "蜂鸣", Description: "按频率档与时长档触发蜂鸣（各 0-9）", InputSchema: buzzerActionSchema},
				},
				Presentation: map[string]any{"defaultWidget": "text"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capLED,
				Version: 1,
				Title:   "LED",
			},
			Spec: model.CapabilitySpec{
				Actions: map[string]model.ActionDecl{
					cmdLED: {Title: "LED", Description: "LED 档 0-9（0=灭，1-8=对应单位 LED，9=全亮）", InputSchema: ledActionSchema},
				},
				Presentation: map[string]any{"defaultWidget": "text"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capDisplay,
				Version: 1,
				Title:   "Display",
			},
			Spec: model.CapabilitySpec{
				Actions: map[string]model.ActionDecl{
					cmdDisplay: {Title: "数码管", Description: "8 位数字显示（每格 0-9）", InputSchema: displayActionSchema},
				},
				Presentation: map[string]any{"defaultWidget": "text"},
			},
		},
		{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata: model.CapabilityMetadata{
				ID:      capMotor,
				Version: 1,
				Title:   "Motor",
			},
			Spec: model.CapabilitySpec{
				Actions: map[string]model.ActionDecl{
					cmdMotor: {Title: "电机", Description: "步进电机档 0-4（0=停）", InputSchema: motorActionSchema},
				},
				Presentation: map[string]any{"defaultWidget": "text"},
			},
		},
	}
}

// Descriptor 返回 STC-B 设备的静态 Descriptor（Entity/Capability 结构 + 身份）。
// 观测值由 dev.Descriptor 在设备实例上填充；identity 由 edge/server 绑定到稳定键。
func (a *Adapter) Descriptor(cfg driverkit.Config) model.Descriptor {
	return stcbDescriptor(cfg.ID)
}

// stcbDescriptor 构造身份 + 实体结构的 Descriptor 骨架（观测值留空）。
func stcbDescriptor(id string) model.Descriptor {
	return model.Descriptor{
		DeviceID:     id,
		ExternalID:   id,
		Manufacturer: "STC",
		Model:        "STC-B (IAP15F2K61S2)",
		Status:       model.DeviceUnavailable,
		Entities: []model.Entity{
			{
				EntityID:     "clock",
				UniqueKey:    "clock",
				Name:         "时钟",
				Category:     model.EntitySensor,
				Capabilities: []string{capClock},
			},
			{
				EntityID:     "alarm",
				UniqueKey:    "alarm",
				Name:         "提醒",
				Category:     model.EntityActuator,
				Capabilities: []string{capAlarm},
			},
			{
				EntityID:     "compartment-1",
				UniqueKey:    "compartment-1",
				Name:         "分格 1",
				Category:     model.EntitySensor,
				Capabilities: []string{capContact},
			},
			{
				EntityID:     "compartment-2",
				UniqueKey:    "compartment-2",
				Name:         "分格 2",
				Category:     model.EntitySensor,
				Capabilities: []string{capContact},
			},
			{
				EntityID:     "compartment-3",
				UniqueKey:    "compartment-3",
				Name:         "分格 3",
				Category:     model.EntitySensor,
				Capabilities: []string{capContact},
			},
			{
				EntityID:     "temperature",
				UniqueKey:    "temperature",
				Name:         "温度",
				Category:     model.EntitySensor,
				Capabilities: []string{capTemp},
			},
			{
				EntityID:     "illuminance",
				UniqueKey:    "illuminance",
				Name:         "光照",
				Category:     model.EntitySensor,
				Capabilities: []string{capIllum},
			},
			{
				EntityID:     "hall",
				UniqueKey:    "hall",
				Name:         "霍尔",
				Category:     model.EntitySensor,
				Capabilities: []string{capHall},
			},
			{
				EntityID:     "vibration",
				UniqueKey:    "vibration",
				Name:         "振动",
				Category:     model.EntitySensor,
				Capabilities: []string{capVib},
			},
			{
				EntityID:     "key",
				UniqueKey:    "key",
				Name:         "按键 K1",
				Category:     model.EntitySensor,
				Capabilities: []string{capKey},
			},
			{
				EntityID:     "buzzer",
				UniqueKey:    "buzzer",
				Name:         "蜂鸣器",
				Category:     model.EntityActuator,
				Capabilities: []string{capBuzzer},
			},
			{
				EntityID:     "led",
				UniqueKey:    "led",
				Name:         "LED",
				Category:     model.EntityActuator,
				Capabilities: []string{capLED},
			},
			{
				EntityID:     "display",
				UniqueKey:    "display",
				Name:         "数码管",
				Category:     model.EntityActuator,
				Capabilities: []string{capDisplay},
			},
			{
				EntityID:     "motor",
				UniqueKey:    "motor",
				Name:         "电机",
				Category:     model.EntityActuator,
				Capabilities: []string{capMotor},
			},
		},
	}
}

// Descriptor 返回带实时观测的完整 Descriptor（Device 级 DescriptorSource 实现）。
// 观测值来自最近一次成功解析的转储；无转储时返回静态骨架（unavailable/offline）。
//
// observed_at 填**该帧转储被解析出来的真实时刻**（d.lastDump）：既不是零值，
// 也不是「现在」硬填——转储可能是几秒前收到的，消费方据此判断观测新鲜度。
// received_at 刻意**不在适配器填**：capability-model.md §4 规定它必须由可信的
// Edge/Core 生成（外部 Driver 插件的时钟与身份都不可信），由 internal/edge 在
// 组装上报信封时统一盖戳。
func (d *dev) Descriptor() model.Descriptor {
	desc := stcbDescriptor(d.id)
	d.mu.Lock()
	dump, lastDump, sensor, lastSensor, dead := d.dump, d.lastDump, d.sensor, d.lastSensor, d.dead
	d.mu.Unlock()

	switch {
	case dump != nil || sensor != nil:
		desc.Status = model.DeviceOnline
	case dead:
		desc.Status = model.DeviceOffline
	default:
		desc.Status = model.DeviceUnavailable
	}

	// 转储帧：时钟（HH:MM）/提醒/三槽；每个观测共享该帧被解析出来的真实时刻。
	if dump != nil {
		at := lastDump
		if at.IsZero() {
			at = time.Now()
		}
		obs := func(capability, property string, value any, unit string) model.Observation {
			return model.Observation{
				Capability: capability, Property: property, Value: value, Unit: unit,
				Quality: model.QualityGood, ObservedAt: at,
			}
		}
		setObservation(&desc, "clock", obs(capClock, "time", fmt.Sprintf("%02d:%02d", dump.Hour, dump.Min), ""))
		setObservation(&desc, "alarm", obs(capAlarm, "state", StateLabel(dump.State), ""))
		for i := range dump.Slots {
			setObservation(&desc, fmt.Sprintf("compartment-%d", i+1), obs(capContact, "state", SlotLabel(dump.Slots[i]), ""))
		}
	}

	// V 帧：全量传感器快照（含秒）。V 帧比 S 转储更新时用 HH:MM:SS 覆盖时钟观测。
	if sensor != nil {
		at := lastSensor
		if at.IsZero() {
			at = time.Now()
		}
		obs := func(capability, property string, value any, unit string) model.Observation {
			return model.Observation{
				Capability: capability, Property: property, Value: value, Unit: unit,
				Quality: model.QualityGood, ObservedAt: at,
			}
		}
		setObservation(&desc, "clock", obs(capClock, "time", fmt.Sprintf("%02d:%02d:%02d", sensor.Hour, sensor.Min, sensor.Sec), ""))
		setObservation(&desc, "temperature", obs(capTemp, "value", TempC(sensor.Rt), "Cel"))
		setObservation(&desc, "illuminance", obs(capIllum, "value", sensor.Rop, ""))
		setObservation(&desc, "hall", obs(capHall, "state", sensor.Hall, ""))
		setObservation(&desc, "vibration", obs(capVib, "state", sensor.Vib, ""))
		setObservation(&desc, "key", obs(capKey, "state", sensor.Key, ""))
	}
	return desc
}

// setObservation 把观测值写入指定 Entity 的 observations（按 property 去重键）。
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
}
