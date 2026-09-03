package model

import "errors"

// Descriptor 是 Edge/Adapter 上报给 Core 与前端 SchemaRenderer 的设备描述，
// 与 spec/descriptor.schema.json 一一对应（A1 生产、A2 消费）。
//
// Descriptor 是 State.Raw 到 typed 模型的迁移载体：Adapter 同时发布 Raw 与
// Descriptor，Core 从 Descriptor 建立 Entity/Capability 注册表，旧 Raw 保留
// 在诊断字段，二者并存。
type Descriptor struct {
	DeviceID     string       `json:"device_id"`
	ExternalID   string       `json:"external_id"`
	Manufacturer string       `json:"manufacturer,omitempty"`
	Model        string       `json:"model,omitempty"`
	Status       DeviceStatus `json:"status"`
	Entities     []Entity     `json:"entities"`
}

// Validate 按 descriptor.schema.json 校验 Descriptor：
// device_id / external_id / status / entities 必填且语义合法，
// entity_id 在设备内唯一，嵌套 Entity / Observation 递归校验。
func (d Descriptor) Validate() error {
	var errs []error
	if d.DeviceID == "" {
		errs = append(errs, fieldErrorf("descriptor", "device_id", "required and must not be empty"))
	}
	if d.ExternalID == "" {
		errs = append(errs, fieldErrorf("descriptor", "external_id", "required and must not be empty"))
	}
	if !d.Status.Valid() {
		errs = append(errs, fieldErrorf("descriptor", "status", "invalid device status %q", d.Status))
	}
	if d.Entities == nil {
		errs = append(errs, fieldErrorf("descriptor", "entities", "required"))
	}
	seen := map[string]bool{}
	for i := range d.Entities {
		e := &d.Entities[i]
		if e.EntityID != "" {
			if seen[e.EntityID] {
				errs = append(errs, fieldErrorf("descriptor", "entities", "duplicate entity_id %q", e.EntityID))
			}
			seen[e.EntityID] = true
		}
		if err := e.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
