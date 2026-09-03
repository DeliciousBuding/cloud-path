package model

import (
	"errors"
	"fmt"
)

// Device 是一台物理或虚拟设备，由 Driver 发现和维护。
//
// device_id 是 Core 内稳定 ID，不得使用串口名（COM3 / /dev/ttyUSB0 会漂移，
// 只能作为当前连接属性）。external_id 是 Driver 范围内不可变 ID。
type Device struct {
	DeviceID         string       `json:"device_id"`
	ExternalID       string       `json:"external_id"`
	PluginInstanceID string       `json:"plugin_instance_id,omitempty"`
	TenantID         string       `json:"tenant_id,omitempty"`
	EdgeID           string       `json:"edge_id,omitempty"`
	Manufacturer     string       `json:"manufacturer,omitempty"`
	Model            string       `json:"model,omitempty"`
	Status           DeviceStatus `json:"status"`
	Entities         []Entity     `json:"entities,omitempty"`
}

// Validate 校验 Device：必需标识非空、状态合法，并递归校验每个 Entity。
// 与 Descriptor 不同，Entities 在完整模型中允许暂缺（发现流程可先建 Device）。
func (d Device) Validate() error {
	var errs []error
	if d.DeviceID == "" {
		errs = append(errs, fieldErrorf("device", "device_id", "must not be empty"))
	}
	if d.ExternalID == "" {
		errs = append(errs, fieldErrorf("device", "external_id", "must not be empty"))
	}
	if !d.Status.Valid() {
		errs = append(errs, fieldErrorf("device", "status", "invalid device status %q", d.Status))
	}
	seen := map[string]bool{}
	for i := range d.Entities {
		e := &d.Entities[i]
		if e.EntityID != "" {
			if seen[e.EntityID] {
				errs = append(errs, fieldErrorf("device", "entities", "duplicate entity_id %q", e.EntityID))
			}
			seen[e.EntityID] = true
		}
		if err := e.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Entity 是 Device 下可独立观察、控制、命名和授权的逻辑单元。
//
// unique_key 由 Driver 生成，用户不可修改；entity_id 是平台稳定 ID，
// 端口/Edge 重连与 Driver 重启都不应改变。
type Entity struct {
	EntityID     string                 `json:"entity_id"`
	UniqueKey    string                 `json:"unique_key"`
	Name         string                 `json:"name,omitempty"`
	Category     EntityCategory         `json:"category"`
	Capabilities []string               `json:"capabilities"`
	Observations map[string]Observation `json:"observations,omitempty"`
}

// Validate 校验 Entity：必需标识非空、分类合法、capability 引用非空，
// 并递归校验内嵌 Observation。
func (e Entity) Validate() error {
	var errs []error
	if e.EntityID == "" {
		errs = append(errs, fieldErrorf("entity", "entity_id", "must not be empty"))
	}
	if e.UniqueKey == "" {
		errs = append(errs, fieldErrorf("entity", "unique_key", "must not be empty"))
	}
	if !e.Category.Valid() {
		errs = append(errs, fieldErrorf("entity", "category", "invalid entity category %q", e.Category))
	}
	for i, c := range e.Capabilities {
		if c == "" {
			errs = append(errs, fieldErrorf("entity", "capabilities", "capability[%d] must not be empty", i))
		}
	}
	for key, obs := range e.Observations {
		if err := obs.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("entity %q observation %q: %w", e.EntityID, key, err))
		}
	}
	return errors.Join(errs...)
}
