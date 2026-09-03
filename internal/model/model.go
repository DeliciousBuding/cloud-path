// Package model 定义 CloudPath 的设备无关领域模型：Device / Entity / Capability /
// Observation / Event / Command / Descriptor，并负责对冻结契约
// spec/descriptor.schema.json 与 spec/capability.schema.json 的校验。
//
// 语义定义以 docs/architecture/capability-model.md 为准。本包只使用标准库，
// 不引入第三方 JSON Schema 依赖；校验器是两份 schema 的忠实 Go 实现。
package model

import "fmt"

// 冻结契约中的 schema 标识。
const (
	// DescriptorSchemaID 是 spec/descriptor.schema.json 的 $id。
	DescriptorSchemaID = "cloudpath.dev/descriptor/v1"
	// CapabilityAPIVersion 是 capability.schema.json 中 apiVersion 的唯一合法值。
	CapabilityAPIVersion = "capabilities.cloudpath.dev/v1alpha1"
	// CapabilityKind 是 capability.schema.json 中 kind 的唯一合法值。
	CapabilityKind = "Capability"
)

// DeviceStatus 是设备在线状态（descriptor.schema.json status 枚举）。
type DeviceStatus string

const (
	DeviceOnline      DeviceStatus = "online"
	DeviceOffline     DeviceStatus = "offline"
	DeviceUnavailable DeviceStatus = "unavailable"
	DeviceDegraded    DeviceStatus = "degraded"
)

// Valid 报告 s 是否为契约允许的设备状态。
func (s DeviceStatus) Valid() bool {
	switch s {
	case DeviceOnline, DeviceOffline, DeviceUnavailable, DeviceDegraded:
		return true
	}
	return false
}

// EntityCategory 是 Entity 的分类。
type EntityCategory string

const (
	EntitySensor     EntityCategory = "sensor"
	EntityActuator   EntityCategory = "actuator"
	EntityDiagnostic EntityCategory = "diagnostic"
	EntityConfig     EntityCategory = "config"
)

// Valid 报告 c 是否为契约允许的 Entity 分类。
func (c EntityCategory) Valid() bool {
	switch c {
	case EntitySensor, EntityActuator, EntityDiagnostic, EntityConfig:
		return true
	}
	return false
}

// Quality 是观测质量等级（Observation 与 Property 共用）。
type Quality string

const (
	QualityGood        Quality = "good"
	QualityUncertain   Quality = "uncertain"
	QualityBad         Quality = "bad"
	QualityUnavailable Quality = "unavailable"
)

// Valid 报告 q 是否为契约允许的质量等级。
func (q Quality) Valid() bool {
	switch q {
	case QualityGood, QualityUncertain, QualityBad, QualityUnavailable:
		return true
	}
	return false
}

// fieldErrorf 构造统一前缀的字段级校验错误，便于调用方按字段定位问题。
func fieldErrorf(subject, field, format string, args ...any) error {
	return fmt.Errorf("model: %s.%s: %s", subject, field, fmt.Sprintf(format, args...))
}
