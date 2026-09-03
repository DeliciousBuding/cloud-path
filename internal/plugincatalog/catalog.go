package plugincatalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

// maxCatalogItems 上限单次列表响应的大小，避免大安装/协调集把响应撑爆。分页留后续。
const maxCatalogItems = 1000

// Observed 是单实例的点时刻观测运行时状态。
type Observed struct {
	HostOnline bool
	State      pluginhost.State
	Health     pluginhost.Health
}

// Reader 是 catalog 的可注入源契约。调用方注入 lockfile/manifest/state/observed/
// metrics 读取器；plugincatalog 本身不构造真实文件系统/Host 源。
//
// nil 或失败的 observed/metrics 读取器不得让插件安装事实变成硬错误：调用方用
// ok=false 表示未观测/失败，catalog 把 observed 标为 unknown。manifest/lock 读取
// 失败属于数据损坏，fail-closed（返回 error，由 caller 映射为 500）。
type Reader interface {
	// Lockfile 返回安装锁文件。
	Lockfile() (*registry.LockFile, error)
	// Manifest 返回 pluginID 的已安装 manifest。
	Manifest(pluginID string) (*registry.Manifest, error)
	// DesiredStates 返回租户的期望实例状态；空 tenant 表示全部租户（开发/全局形态）。
	DesiredStates(tenant string) ([]plugincontrol.InstanceState, error)
	// Observed 返回单实例的观测运行时状态；ok=false 表示 Host 未运行或无法观测。
	Observed(tenant, instanceID string) (Observed, bool)
	// Metrics 返回点时刻资源指标；ok=false 表示不可用。
	Metrics(tenant, instanceID string) (pluginhost.Metrics, bool)
}

// Catalog 是 server 消费的只读 API 视图接口。实现可被注入，nil 时 server 返回空列表。
type Catalog interface {
	Plugins(tenant string) ([]PluginView, error)
	Plugin(tenant, id string) (PluginView, bool, error)
	Instances(tenant string) ([]InstanceView, error)
	Instance(tenant, id string) (InstanceView, bool, error)
}

// catalog 是 Catalog 的默认实现，依赖注入的 Reader。
type catalog struct {
	reader Reader
}

// New 构造 catalog；reader 为 nil 时返回空 catalog（不 panic，返回空列表/未找到）。
func New(reader Reader) Catalog {
	if reader == nil {
		return emptyCatalog{}
	}
	return &catalog{reader: reader}
}

func (c *catalog) Plugins(tenant string) ([]PluginView, error) {
	lock, err := c.reader.Lockfile()
	if err != nil {
		return nil, fmt.Errorf("plugincatalog: load lockfile: %w", err)
	}
	views := make([]PluginView, 0, len(lock.Plugins))
	for i := range lock.Plugins {
		view, err := c.pluginView(lock.Plugins[i])
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return capList(views), nil
}

func (c *catalog) Plugin(tenant, id string) (PluginView, bool, error) {
	if id == "" {
		return PluginView{}, false, nil
	}
	lock, err := c.reader.Lockfile()
	if err != nil {
		return PluginView{}, false, fmt.Errorf("plugincatalog: load lockfile: %w", err)
	}
	entry, ok := lock.Find(id)
	if !ok {
		return PluginView{}, false, nil
	}
	view, err := c.pluginView(*entry)
	if err != nil {
		return PluginView{}, false, err
	}
	return view, true, nil
}

func (c *catalog) pluginView(entry registry.LockedPlugin) (PluginView, error) {
	view := PluginView{
		ID: entry.ID, Version: entry.Version, Source: entry.Source,
		Digest: entry.Digest, Verified: entry.Verified,
		Compatibility: entry.Compatibility, Protocol: entry.Protocol,
	}
	manifest, err := c.reader.Manifest(entry.ID)
	if err != nil {
		return PluginView{}, fmt.Errorf("plugincatalog: load manifest %s: %w", entry.ID, err)
	}
	if manifest != nil {
		if manifest.Kind != "" {
			view.Kind = manifest.Kind
		}
		if manifest.Version != "" {
			view.Version = manifest.Version
		}
		if manifest.Protocol != 0 {
			view.Protocol = manifest.Protocol
		}
		if manifest.Compatibility.Core != "" {
			view.Compatibility = manifest.Compatibility.Core
		}
		view.Permissions = projectPermissions(manifest.Permissions)
		view.Contributes = projectContributes(manifest.Contributes)
	}
	return view, nil
}

func (c *catalog) Instances(tenant string) ([]InstanceView, error) {
	states, err := c.reader.DesiredStates(tenant)
	if err != nil {
		return nil, fmt.Errorf("plugincatalog: load desired states: %w", err)
	}
	views := make([]InstanceView, 0, len(states))
	for i := range states {
		views = append(views, c.instanceView(states[i]))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return capList(views), nil
}

func (c *catalog) Instance(tenant, id string) (InstanceView, bool, error) {
	if id == "" {
		return InstanceView{}, false, nil
	}
	states, err := c.reader.DesiredStates(tenant)
	if err != nil {
		return InstanceView{}, false, fmt.Errorf("plugincatalog: load desired states: %w", err)
	}
	for i := range states {
		if states[i].InstanceID == id {
			return c.instanceView(states[i]), true, nil
		}
	}
	return InstanceView{}, false, nil
}

func (c *catalog) instanceView(state plugincontrol.InstanceState) InstanceView {
	view := InstanceView{
		Tenant: state.Tenant, ID: state.InstanceID, Plugin: state.PluginID,
		Version: state.Version, DesiredEnabled: state.Enabled,
		ConfigPresent: state.ConfigPath != "",
		// catalog 中的实例未被 purge（purge 会删除状态记录），默认保留数据。
		DataPreserved: true,
	}
	obs, ok := c.reader.Observed(state.Tenant, state.InstanceID)
	if !ok || !obs.HostOnline {
		view.ObservedState = "unknown"
		view.Health = "unknown"
	} else {
		view.ObservedState = obs.State.String()
		view.Health = obs.Health.String()
	}
	metrics, mok := c.reader.Metrics(state.Tenant, state.InstanceID)
	view.Metrics = projectMetrics(metrics, mok)
	return view
}

// capList 统一裁剪列表到响应大小上限。
func capList[T any](in []T) []T {
	if len(in) > maxCatalogItems {
		return in[:maxCatalogItems]
	}
	return in
}

func projectPermissions(p registry.Permissions) PermissionsView {
	return PermissionsView{
		Hardware: p.Hardware, Network: p.Network,
		Filesystem: p.Filesystem, Secrets: p.Secrets,
	}
}

func projectContributes(c *registry.Contributes) ContributesView {
	var out ContributesView
	if c == nil {
		return out
	}
	for _, d := range c.Drivers {
		out.Drivers = append(out.Drivers, DriverContributionView{
			ID: d.ID, Title: d.Title,
			Descriptor:        safeRef(d.Descriptor),
			ConfigSchema:      safeRef(d.ConfigSchema),
			Discovery:         d.Discovery,
			CapabilityCatalog: safeRef(d.CapabilityCatalog),
		})
	}
	for _, a := range c.Applications {
		out.Applications = append(out.Applications, ApplicationContributionView{ID: a.ID, Title: a.Title})
	}
	for _, conn := range c.Connectors {
		out.Connectors = append(out.Connectors, ConnectorContributionView{
			ID: conn.ID, Title: conn.Title, Direction: conn.Direction, Host: conn.Host,
		})
	}
	return out
}

// safeRef 只保留相对引用；本地绝对路径（盘符/前导斜杠/UNC）被丢弃。
func safeRef(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "\\") {
		return ""
	}
	if len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		return ""
	}
	return s
}

func projectMetrics(m pluginhost.Metrics, ok bool) MetricsView {
	if !ok {
		return MetricsView{Handles: -1, Goroutines: -1}
	}
	return MetricsView{
		CPUTime:      m.CPUTime.Milliseconds(),
		RSSBytes:     m.RSSBytes,
		Handles:      m.Handles,
		Goroutines:   m.Goroutines,
		MessageRate:  m.MessageRate,
		RestartCount: m.RestartCount,
		LastHealthy:  m.LastHealthy.Unix(),
	}
}

// SourceReader 是由可注入函数字段组装的 Reader。nil 字段按空/不可用处理，调用方
// 只提供手头有的源（例如无运行中的 Host 时 observed/metrics 恒为 unknown）。
type SourceReader struct {
	LockfileFn func() (*registry.LockFile, error)
	ManifestFn func(pluginID string) (*registry.Manifest, error)
	DesiredFn  func(tenant string) ([]plugincontrol.InstanceState, error)
	ObservedFn func(tenant, instanceID string) (Observed, bool)
	MetricsFn  func(tenant, instanceID string) (pluginhost.Metrics, bool)
}

func (r *SourceReader) Lockfile() (*registry.LockFile, error) {
	if r.LockfileFn == nil {
		return registry.NewLockFile(), nil
	}
	return r.LockfileFn()
}

func (r *SourceReader) Manifest(pluginID string) (*registry.Manifest, error) {
	if r.ManifestFn == nil {
		return nil, fmt.Errorf("plugincatalog: no manifest source")
	}
	return r.ManifestFn(pluginID)
}

func (r *SourceReader) DesiredStates(tenant string) ([]plugincontrol.InstanceState, error) {
	if r.DesiredFn == nil {
		return nil, nil
	}
	return r.DesiredFn(tenant)
}

func (r *SourceReader) Observed(tenant, instanceID string) (Observed, bool) {
	if r.ObservedFn == nil {
		return Observed{}, false
	}
	return r.ObservedFn(tenant, instanceID)
}

func (r *SourceReader) Metrics(tenant, instanceID string) (pluginhost.Metrics, bool) {
	if r.MetricsFn == nil {
		return pluginhost.Metrics{}, false
	}
	return r.MetricsFn(tenant, instanceID)
}

// emptyCatalog 是 New(nil) 的结果：返回空列表/未找到，永不 panic。
type emptyCatalog struct{}

func (emptyCatalog) Plugins(string) ([]PluginView, error)         { return []PluginView{}, nil }
func (emptyCatalog) Plugin(_, _ string) (PluginView, bool, error) { return PluginView{}, false, nil }
func (emptyCatalog) Instances(string) ([]InstanceView, error)     { return []InstanceView{}, nil }
func (emptyCatalog) Instance(_, _ string) (InstanceView, bool, error) {
	return InstanceView{}, false, nil
}
