// Package plugincatalog 是插件事实的只读、租户作用域视图：合并 Registry
// lockfile/manifest（安装事实）、plugincontrol desired state（期望状态）、
// pluginhost observed snapshot/metrics（观测状态），为 WebUI 与自动化提供稳定 API。
//
// 安全边界（docs/api.md 插件目录）：视图只携带白名单字段，绝不返回安装本地绝对路径、
// proof、env 或 config secret 值；desired 与 observed 严格分离，Host 未运行时
// observed 为 unknown，绝不根据 desired_enabled 虚报 HEALTHY。
package plugincatalog

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
	ID                string `json:"id"`
	Title             string `json:"title,omitempty"`
	Descriptor        string `json:"descriptor,omitempty"`
	ConfigSchema      string `json:"configSchema,omitempty"`
	Discovery         string `json:"discovery,omitempty"`
	CapabilityCatalog string `json:"capabilityCatalog,omitempty"`
}

// ApplicationContributionView 是一个 Application 贡献。requirements 可能携带任意
// 载荷，为避免泄漏不暴露，仅保留 id/title。
type ApplicationContributionView struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// ConnectorContributionView 是一个 Connector 贡献。
type ConnectorContributionView struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Direction string `json:"direction,omitempty"`
	Host      string `json:"host,omitempty"`
}

// InstanceView 是单个插件实例的期望+观测视图。desired_enabled 与 observed_state
// 相互独立；Host 未运行时 observed_state/health 为 unknown。
type InstanceView struct {
	Tenant         string `json:"tenant"`
	EdgeID         string `json:"edge_id"`
	ID             string `json:"id"`
	Plugin         string `json:"plugin"`
	Version        string `json:"version"`
	DesiredEnabled bool   `json:"desired_enabled"`
	ObservedState  string `json:"observed_state"`
	Health         string `json:"health"`
	DataPreserved  bool   `json:"data_preserved"`
	ConfigPresent  bool   `json:"config_present"`
	// Drift 表示 desired_revision 与 applied_revision 不一致（期望已变、Edge 未确认）。
	Drift bool `json:"drift"`
	// Stale 表示 observed 投影过期或所属 edge 离线；只标记，绝不改写 desired。
	Stale   bool        `json:"stale"`
	Metrics MetricsView `json:"metrics"`
}

// MetricsView 是实例的脱敏资源指标快照。不可观测字段以 -1 标记 unavailable。
type MetricsView struct {
	CPUTime      int64   `json:"cpu_ms"`
	RSSBytes     int64   `json:"rss_bytes"`
	Handles      int     `json:"handles"`
	Goroutines   int     `json:"goroutines"`
	MessageRate  float64 `json:"message_rate"`
	RestartCount int     `json:"restart_count"`
	LastHealthy  int64   `json:"last_healthy"`
}
