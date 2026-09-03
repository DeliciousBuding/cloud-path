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
)

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
	dump, dead, last := d.dump, d.dead, d.lastDump
	d.mu.Unlock()

	switch {
	case dump != nil:
		desc.Status = model.DeviceOnline
		// 同一帧转储解析出的全部观测共享同一个真实采集时刻。
		at := last
		if at.IsZero() {
			at = time.Now()
		}
		obs := func(capability, property string, value any) model.Observation {
			return model.Observation{
				Capability: capability, Property: property, Value: value,
				Quality: model.QualityGood, ObservedAt: at,
			}
		}
		setObservation(&desc, "clock", obs(capClock, "time", fmt.Sprintf("%02d:%02d", dump.Hour, dump.Min)))
		setObservation(&desc, "alarm", obs(capAlarm, "state", StateLabel(dump.State)))
		for i := range dump.Slots {
			setObservation(&desc, fmt.Sprintf("compartment-%d", i+1), obs(capContact, "state", SlotLabel(dump.Slots[i])))
		}
	case dead:
		desc.Status = model.DeviceOffline
	default:
		desc.Status = model.DeviceUnavailable
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
