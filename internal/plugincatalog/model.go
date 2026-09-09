// Package plugincatalog 是插件事实的只读、租户作用域视图。安装物视图来自
// Edge 上报投影；实例视图由 InstanceViews 从 Server 的 desired + observed
// 投影构造，为 WebUI 与自动化提供稳定 API。
//
// 安全边界（docs/api.md 插件目录）：视图只携带白名单字段，绝不返回安装本地绝对路径、
// proof、env 或 config secret 值；desired 与 observed 严格分离，Host 未运行时
// observed 为 unknown，绝不根据 desired_enabled 虚报 HEALTHY。
package plugincatalog

import "github.com/DeliciousBuding/cloud-path/internal/api"

// PluginView 是单个已安装插件的脱敏事实视图。
type PluginView struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	Version       string          `json:"version"`
	Source        string          `json:"source"`
	Digest        string          `json:"digest"`
	Verified      bool            `json:"verified"`
	Compatibility string          `json:"compatibility,omitempty"`
	Protocol      int             `json:"protocol"`
	Permissions   PermissionsView `json:"permissions"`
	Contributes   ContributesView `json:"contributes"`
}

// PermissionsView 是权限披露（请求的名字，非 secret 值）。
type PermissionsView struct {
	Hardware   []string `json:"hardware,omitempty"`
	Network    []string `json:"network,omitempty"`
	Filesystem []string `json:"filesystem,omitempty"`
	Secrets    []string `json:"secrets,omitempty"`
}

// ContributesView 是插件声明的贡献。路径类字段只保留相对引用，本地绝对路径被丢弃。
type ContributesView struct {
	Drivers      []DriverContributionView      `json:"drivers,omitempty"`
	Applications []ApplicationContributionView `json:"applications,omitempty"`
	Connectors   []ConnectorContributionView   `json:"connectors,omitempty"`
}

// DriverContributionView 是一个 Driver 贡献。
type DriverContributionView struct {
	ID                string            `json:"id"`
	Title             string            `json:"title,omitempty"`
	Descriptor        string            `json:"descriptor,omitempty"`
	ConfigSchema      string            `json:"configSchema,omitempty"`
	Discovery         string            `json:"discovery,omitempty"`
	CapabilityCatalog string            `json:"capabilityCatalog,omitempty"`
	UI                *api.PluginUIData `json:"ui,omitempty"`
}

// ApplicationContributionView 是一个 Application 贡献。requirements 可能携带任意
// 载荷，为避免泄漏不暴露，仅保留 id/title。
type ApplicationContributionView struct {
	ID    string            `json:"id"`
	Title string            `json:"title,omitempty"`
	UI    *api.PluginUIData `json:"ui,omitempty"`
}

// ConnectorContributionView 是一个 Connector 贡献。
type ConnectorContributionView struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Direction string `json:"direction,omitempty"`
	Host      string `json:"host,omitempty"`
}
