package model

import (
	"errors"
	"time"
)

// Observation 是可形成当前状态的观测值（数据三分法之一）。
//
// observed_at 可来自设备；received_at 必须由可信的 Edge/Core 生成。
// 设备时钟不可信时以 received_at 为准并标注质量。sequence 是单调递增序号，
// 供乱序检测与去重。
type Observation struct {
	EntityID   string    `json:"entity_id,omitempty"`
	Capability string    `json:"capability"`
	Property   string    `json:"property"`
	Value      any       `json:"value"`
	Unit       string    `json:"unit,omitempty"`
	Quality    Quality   `json:"quality,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
	ReceivedAt time.Time `json:"received_at,omitempty"`
	Sequence   int64     `json:"sequence,omitempty"`
}

// Validate 按 descriptor.schema.json 的 Observation 定义校验。
// value 为必填键：JSON null 视同缺失并拒绝（调用方应以真实数据构造）。
// observed_at / received_at 的 RFC3339 合法性在 JSON 解码时由 time.Time 保证。
func (o Observation) Validate() error {
	var errs []error
	if o.Capability == "" {
		errs = append(errs, fieldErrorf("observation", "capability", "required and must not be empty"))
	}
	if o.Property == "" {
		errs = append(errs, fieldErrorf("observation", "property", "required and must not be empty"))
	}
	if o.Value == nil {
		errs = append(errs, fieldErrorf("observation", "value", "required"))
	}
	if o.Quality != "" && !o.Quality.Valid() {
		errs = append(errs, fieldErrorf("observation", "quality", "invalid quality %q", o.Quality))
	}
	return errors.Join(errs...)
}
