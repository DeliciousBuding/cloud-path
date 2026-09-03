package model

import (
	"errors"
	"time"
)

// Event 是不可覆盖的时间点事实（如 opened、alarm-fired、device-booted）。
//
// Type 属于 Capability 或 Application 命名空间（例如
// cloudpath.dev/capability/contact@1/opened），不维护平台级业务事件枚举。
// EntityID 对设备级事件（device-booted）可省略。
type Event struct {
	EntityID   string         `json:"entity_id,omitempty"`
	Type       string         `json:"type"`
	OccurredAt time.Time      `json:"occurred_at"`
	ReceivedAt time.Time      `json:"received_at,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}

// Validate 校验 Event：type 非空、occurred_at 必须存在。
func (e Event) Validate() error {
	var errs []error
	if e.Type == "" {
		errs = append(errs, fieldErrorf("event", "type", "required and must not be empty"))
	}
	if e.OccurredAt.IsZero() {
		errs = append(errs, fieldErrorf("event", "occurred_at", "required"))
	}
	return errors.Join(errs...)
}
