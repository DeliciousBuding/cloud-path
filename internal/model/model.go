// Package model 是 sdk/go/model 的内部薄兼容层。领域模型的单一实现已迁到公共 SDK，
// internal/* 通过这里的类型别名与常量继续使用，禁止在本包复制会漂移的结构。
//
// 语义定义以 docs/architecture/capability-model.md 为准。
package model

import (
	"time"

	sdkmodel "github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// 冻结契约中的 schema 标识与 Capability 常量。
const (
	DescriptorSchemaID   = sdkmodel.DescriptorSchemaID
	CapabilityAPIVersion = sdkmodel.CapabilityAPIVersion
	CapabilityKind       = sdkmodel.CapabilityKind
)

// DeviceStatus 是设备在线状态（descriptor.schema.json status 枚举）。
type DeviceStatus = sdkmodel.DeviceStatus

const (
	DeviceOnline      = sdkmodel.DeviceOnline
	DeviceOffline     = sdkmodel.DeviceOffline
	DeviceUnavailable = sdkmodel.DeviceUnavailable
	DeviceDegraded    = sdkmodel.DeviceDegraded
)

// EntityCategory 是 Entity 的分类。
type EntityCategory = sdkmodel.EntityCategory

const (
	EntitySensor     = sdkmodel.EntitySensor
	EntityActuator   = sdkmodel.EntityActuator
	EntityDiagnostic = sdkmodel.EntityDiagnostic
	EntityConfig     = sdkmodel.EntityConfig
)

// Quality 是观测质量等级（Observation 与 Property 共用）。
type Quality = sdkmodel.Quality

const (
	QualityGood        = sdkmodel.QualityGood
	QualityUncertain   = sdkmodel.QualityUncertain
	QualityBad         = sdkmodel.QualityBad
	QualityUnavailable = sdkmodel.QualityUnavailable
)

// CommandStatus 是 Command 生命周期状态。
type CommandStatus = sdkmodel.CommandStatus

const (
	CommandCreated    = sdkmodel.CommandCreated
	CommandDispatched = sdkmodel.CommandDispatched
	CommandAccepted   = sdkmodel.CommandAccepted
	CommandRunning    = sdkmodel.CommandRunning
	CommandSucceeded  = sdkmodel.CommandSucceeded
	CommandFailed     = sdkmodel.CommandFailed
	CommandTimedOut   = sdkmodel.CommandTimedOut
	CommandCancelled  = sdkmodel.CommandCancelled
)

// PropertyAccess 是 Property 的读写权限。
type PropertyAccess = sdkmodel.PropertyAccess

const (
	PropertyRead      = sdkmodel.PropertyRead
	PropertyWrite     = sdkmodel.PropertyWrite
	PropertyReadWrite = sdkmodel.PropertyReadWrite
)

// 领域模型类型（与 sdk/go/model 完全一致）。
type (
	Device             = sdkmodel.Device
	Entity             = sdkmodel.Entity
	Event              = sdkmodel.Event
	Observation        = sdkmodel.Observation
	Command            = sdkmodel.Command
	Capability         = sdkmodel.Capability
	CapabilityMetadata = sdkmodel.CapabilityMetadata
	CapabilitySpec     = sdkmodel.CapabilitySpec
	Property           = sdkmodel.Property
	EventDecl          = sdkmodel.EventDecl
	ActionDecl         = sdkmodel.ActionDecl
	Descriptor         = sdkmodel.Descriptor
)

// NewCommand 构造一条 CREATED 状态的 Command。
func NewCommand(commandID, idempotencyKey, entityID, action string, args map[string]any, deadline time.Time, actor string) Command {
	return sdkmodel.NewCommand(commandID, idempotencyKey, entityID, action, args, deadline, actor)
}
