package model

import (
	"errors"
	"regexp"
)

// capabilityIDPattern 是 capability.schema.json metadata.id 的 pattern：
// "<publisher>/capability/<name>@<version>"，例如
// cloudpath.dev/capability/temperature@1 或
// io.github.example/capability/air-quality-index@1。
var capabilityIDPattern = regexp.MustCompile(`^.+/capability/.+@\d+$`)

// Capability 描述 Entity「能做什么」，由稳定 ID 与独立版本标识，
// 对应 spec/capability.schema.json。未知 Capability 仍可保存和转发，
// 只是 Core UI 回落为通用 JSON/表格视图。
type Capability struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Metadata   CapabilityMetadata `json:"metadata"`
	Spec       CapabilitySpec     `json:"spec"`
}

// CapabilityMetadata 是 Capability 的标识元数据。机器 ID 不得本地化，
// 破坏性变化发布 @2 而不是原地改语义。
type CapabilityMetadata struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Title   string `json:"title,omitempty"`
}

// CapabilitySpec 声明 Properties / Events / Actions 与 UI Hints。
// presentation 只是渲染提示，不是语义真相。
type CapabilitySpec struct {
	Properties   map[string]Property   `json:"properties,omitempty"`
	Events       map[string]EventDecl  `json:"events,omitempty"`
	Actions      map[string]ActionDecl `json:"actions,omitempty"`
	Presentation map[string]any        `json:"presentation,omitempty"`
}

// Property 是一个数据点声明。
type Property struct {
	Type    string         `json:"type,omitempty"`
	Unit    string         `json:"unit,omitempty"` // 统一单位代码，不得用本地化文本
	Access  PropertyAccess `json:"access,omitempty"`
	Quality []Quality      `json:"quality,omitempty"`
}

// PropertyAccess 是 Property 的读写权限。
type PropertyAccess string

const (
	PropertyRead      PropertyAccess = "read"
	PropertyWrite     PropertyAccess = "write"
	PropertyReadWrite PropertyAccess = "readwrite"
)

// Valid 报告 a 是否为契约允许的访问模式。空值表示未声明（可选缺省）。
func (a PropertyAccess) Valid() bool {
	switch a {
	case PropertyRead, PropertyWrite, PropertyReadWrite:
		return true
	}
	return false
}

// EventDecl 声明一个事件及可选 payloadSchema（JSON Schema 可表达子集）。
type EventDecl struct {
	PayloadSchema map[string]any `json:"payloadSchema,omitempty"`
}

// ActionDecl 声明一个动作及可选 title/description/inputSchema。
// title/description 是纯呈现提示（schema-driven UI 用它们渲染说明与参数控件），
// 不是语义真相；前端不得因其缺失而存不到可下发命令（命令集仍以 action 键为准）。
type ActionDecl struct {
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// Validate 按 capability.schema.json 校验 Capability：
// apiVersion / kind 常量、metadata.id pattern、version 非负，
// property access / quality 枚举合法。
func (c Capability) Validate() error {
	var errs []error
	if c.APIVersion != CapabilityAPIVersion {
		errs = append(errs, fieldErrorf("capability", "apiVersion", "must be %q, got %q", CapabilityAPIVersion, c.APIVersion))
	}
	if c.Kind != CapabilityKind {
		errs = append(errs, fieldErrorf("capability", "kind", "must be %q, got %q", CapabilityKind, c.Kind))
	}
	if c.Metadata.ID == "" {
		errs = append(errs, fieldErrorf("capability", "metadata.id", "required and must not be empty"))
	} else if !capabilityIDPattern.MatchString(c.Metadata.ID) {
		errs = append(errs, fieldErrorf("capability", "metadata.id", "%q does not match <publisher>/capability/<name>@<version>", c.Metadata.ID))
	}
	if c.Metadata.Version < 0 {
		errs = append(errs, fieldErrorf("capability", "metadata.version", "must be non-negative, got %d", c.Metadata.Version))
	}
	for name, p := range c.Spec.Properties {
		if p.Access != "" && !p.Access.Valid() {
			errs = append(errs, fieldErrorf("capability", "spec.properties", "property %q: invalid access %q", name, p.Access))
		}
		for i, q := range p.Quality {
			if !q.Valid() {
				errs = append(errs, fieldErrorf("capability", "spec.properties", "property %q: invalid quality %q at index %d", name, q, i))
			}
		}
	}
	return errors.Join(errs...)
}
