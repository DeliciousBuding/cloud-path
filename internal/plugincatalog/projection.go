package plugincatalog

import (
	"fmt"
	"sort"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// ProjectionInstance 是 Server 侧「desired + observed」合成后的单实例事实。
//
// 不变量（docs/architecture/control-plane-sync.md §2）：
//   - HasObserved=false 表示 Edge 从未上报过该实例，Observed 必须为 null，
//     绝不把 desired enabled 渲染成 observed healthy；
//   - Drift = desired_revision != applied_revision（期望已变但 Edge 未确认收敛）；
//   - Stale = 投影过期或所属 edge 离线（只标记，不改写 desired）。
type ProjectionInstance struct {
	TenantID   int64
	Tenant     string
	EdgeID     string
	InstanceID string
	PluginID   string
	Version    string
	Enabled    bool
	Isolation  string
	// Config 只含非敏感标量与 secret://<name> handle（写入时已拒绝明文凭据）。
	Config map[string]string
	// ConfigPresent 只表示「有非敏感配置」，绝不携带 secret 明文。
	ConfigPresent bool
	// SecretRefs 只含 secret://<name> handle 名。
	SecretRefs []string

	HasObserved bool
	// ObservedVersion 是 Edge 上报的实际版本，与 desired Version 分别承载：
	// 只有 observed 的实例（Server 无期望态）不得把上报值塞进 desired 子视图。
	ObservedVersion string
	State           string
	Health          string
	Detail          string
	RestartCount    int
	LastHealthy     int64
	ReportedAt      int64
	MessageRate     float64

	EdgeOnline      bool
	DesiredRevision uint64
	AppliedRevision uint64
	Drift           bool
	Stale           bool
	LastAckAt       int64
	UpdatedAt       int64
	RowRevision     uint64
}

// ProjectionSource 是 Server 投影的只读源（由 internal/server 实现并注入）。
// tenant 为租户 slug，与 Catalog 接口保持一致；空串表示全部租户（开发/全局形态）。
//
// 实现必须是真实投影：安装物只来自 Edge 上报，期望态只来自 Server 权威存储。
// 禁止用静态/内置样例数据填充。
type ProjectionSource interface {
	// Installations 返回租户可见的已安装插件公开事实（Edge 上报投影）。
	Installations(tenant string) ([]api.PluginInstallationStatusData, error)
	// Instances 返回租户可见的插件实例合成事实（desired + observed）。
	Instances(tenant string) ([]ProjectionInstance, error)
}

// NewProjectionCatalog 用 Server 投影构造只读目录，替换任何 fake/静态来源。
// src 为 nil 时返回空目录（永不 panic）。
func NewProjectionCatalog(src ProjectionSource) Catalog {
	if src == nil {
		return emptyCatalog{}
	}
	return &projectionCatalog{src: src}
}

type projectionCatalog struct{ src ProjectionSource }

func (c *projectionCatalog) Plugins(tenant string) ([]PluginView, error) {
	rows, err := c.src.Installations(tenant)
	if err != nil {
		return nil, err
	}
	views := make([]PluginView, 0, len(rows))
	for _, in := range rows {
		views = append(views, pluginViewFromInstallation(in))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	if err := validateUIRouteConflicts(views); err != nil {
		return nil, err
	}
	return capList(views), nil
}

func (c *projectionCatalog) Plugin(tenant, id string) (PluginView, bool, error) {
	if id == "" {
		return PluginView{}, false, nil
	}
	rows, err := c.src.Installations(tenant)
	if err != nil {
		return PluginView{}, false, err
	}
	for _, in := range rows {
		if in.PluginID == id {
			return pluginViewFromInstallation(in), true, nil
		}
	}
	return PluginView{}, false, nil
}

// InstanceViews 构造契约视图（GET /api/plugin-instances 的载荷）。
// desired 与 observed 永远分开呈现：无 observed 时 Observed 为 nil。
func InstanceViews(src ProjectionSource, tenant string) ([]api.PluginInstanceView, error) {
	if src == nil {
		return []api.PluginInstanceView{}, nil
	}
	rows, err := src.Instances(tenant)
	if err != nil {
		return nil, err
	}
	out := make([]api.PluginInstanceView, 0, len(rows))
	for _, in := range rows {
		out = append(out, APIInstanceView(in))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EdgeID != out[j].EdgeID {
			return out[i].EdgeID < out[j].EdgeID
		}
		return out[i].ID < out[j].ID
	})
	return capList(out), nil
}

// APIInstanceView 是单实例的契约视图映射（纯函数，便于反向测试）。
func APIInstanceView(in ProjectionInstance) api.PluginInstanceView {
	view := api.PluginInstanceView{
		ID: in.InstanceID, TenantID: in.TenantID, EdgeID: in.EdgeID,
		Desired: api.PluginInstanceDesiredView{
			InstanceID: in.InstanceID, PluginID: in.PluginID, Version: in.Version,
			Enabled: in.Enabled, Isolation: in.Isolation, Config: cloneConfig(in.Config),
			SecretRefs: append([]string(nil), in.SecretRefs...),
			Revision:   in.RowRevision, UpdatedAt: in.UpdatedAt,
		},
		HasObserved:     in.HasObserved,
		EdgeOnline:      in.EdgeOnline,
		DesiredRevision: in.DesiredRevision,
		AppliedRevision: in.AppliedRevision,
		Drift:           in.Drift,
		Stale:           in.Stale,
		LastAckAt:       in.LastAckAt,
	}
	if len(view.Desired.SecretRefs) == 0 {
		view.Desired.SecretRefs = nil
	}
	if in.HasObserved {
		observedVersion := in.ObservedVersion
		if observedVersion == "" {
			observedVersion = in.Version
		}
		view.Observed = &api.PluginInstanceObservedView{
			State: in.State, Health: in.Health, Version: observedVersion,
			Detail: SanitizeDetail(in.Detail), RestartCount: in.RestartCount,
			LastHealthy: in.LastHealthy, ReportedAt: in.ReportedAt,
		}
	}
	return view
}

// pluginViewFromInstallation 把 Edge 上报的安装物投影映射为脱敏插件视图。
// 只使用公开 manifest 字段：Source/Compatibility 不在上报范围内，留空而不是编造。
func pluginViewFromInstallation(in api.PluginInstallationStatusData) PluginView {
	return PluginView{
		ID: in.PluginID, Kind: in.Kind, Version: in.Version,
		Digest: in.Digest, Verified: in.Verified, Protocol: in.Protocol,
		Permissions: PermissionsView{
			Hardware: in.Permissions.Hardware, Network: in.Permissions.Network,
			Filesystem: in.Permissions.Filesystem, Secrets: in.Permissions.Secrets,
		},
		Contributes: ContributesView{
			Drivers:      driverViews(in.Contributions.Drivers),
			Applications: applicationViews(in.Contributions.Applications),
			Connectors:   connectorViews(in.Contributions.Connectors),
		},
	}
}

func driverViews(in []api.PluginDriverContributionData) []DriverContributionView {
	if len(in) == 0 {
		return nil
	}
	out := make([]DriverContributionView, 0, len(in))
	for _, d := range in {
		out = append(out, DriverContributionView{ID: d.ID, Title: d.Title, I18n: d.I18n, Discovery: d.Discovery, UI: clonePluginUI(d.UI)})
	}
	return out
}

func applicationViews(in []api.PluginApplicationContributionData) []ApplicationContributionView {
	if len(in) == 0 {
		return nil
	}
	out := make([]ApplicationContributionView, 0, len(in))
	for _, a := range in {
		out = append(out, ApplicationContributionView{ID: a.ID, Title: a.Title, I18n: a.I18n, UI: clonePluginUI(a.UI)})
	}
	return out
}

func connectorViews(in []api.PluginConnectorContributionData) []ConnectorContributionView {
	if len(in) == 0 {
		return nil
	}
	out := make([]ConnectorContributionView, 0, len(in))
	for _, c := range in {
		out = append(out, ConnectorContributionView{ID: c.ID, Title: c.Title, I18n: c.I18n, Direction: c.Direction, Host: c.Host})
	}
	return out
}

// cloneConfig 复制配置 map，避免把内部缓存的引用透出到响应。
func cloneConfig(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneI18n(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// validateUIRouteConflicts fails the whole catalog projection when two visible
// Application contributions claim the same stable route. Silent last-writer
// wins would make navigation non-deterministic and is never acceptable.
func validateUIRouteConflicts(views []PluginView) error {
	seen := map[string]string{}
	for _, plugin := range views {
		for _, app := range plugin.Contributes.Applications {
			if app.UI == nil || app.UI.Navigation == nil || app.UI.Navigation.Route == "" {
				continue
			}
			route := app.UI.Navigation.Route
			if previous, exists := seen[route]; exists && previous != plugin.ID {
				return fmt.Errorf("plugin UI route %q conflicts between %s and %s", route, previous, plugin.ID)
			}
			seen[route] = plugin.ID
		}
	}
	return nil
}

func clonePluginUI(in *api.PluginUIData) *api.PluginUIData {
	if in == nil {
		return nil
	}
	out := &api.PluginUIData{APIVersion: in.APIVersion}
	if in.Navigation != nil {
		n := *in.Navigation
		n.I18n = cloneI18n(n.I18n)
		out.Navigation = &n
	}
	if len(in.Pages) > 0 {
		out.Pages = make([]api.PluginUIPageData, 0, len(in.Pages))
		for _, page := range in.Pages {
			out.Pages = append(out.Pages, api.PluginUIPageData{
				ID: page.ID, Title: page.Title, I18n: cloneI18n(page.I18n), Sections: cloneUISections(page.Sections),
			})
		}
	}
	if in.Device != nil {
		out.Device = &api.PluginUIDeviceData{Sections: cloneUISections(in.Device.Sections)}
	}
	return out
}

func cloneUISections(in []api.PluginUISectionData) []api.PluginUISectionData {
	if len(in) == 0 {
		return nil
	}
	out := make([]api.PluginUISectionData, 0, len(in))
	for _, section := range in {
		clone := section
		clone.Text = SanitizeDetail(section.Text)
		clone.Scopes = append([]string(nil), section.Scopes...)
		clone.I18n = cloneI18n(section.I18n)
		clone.Fields = sanitizeUIFields(section.Fields)
		out = append(out, clone)
	}
	return out
}

func sanitizeUIFields(in []map[string]any) []map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(in))
	for _, field := range in {
		if field == nil {
			out = append(out, nil)
			continue
		}
		clone := make(map[string]any, len(field))
		for k, v := range field {
			clone[k] = sanitizeUIValue(v)
		}
		out = append(out, clone)
	}
	return out
}

func sanitizeUIValue(v any) any {
	switch value := v.(type) {
	case string:
		return SanitizeDetail(value)
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = sanitizeUIValue(value[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, child := range value {
			out[k] = sanitizeUIValue(child)
		}
		return out
	default:
		return v
	}
}
