package plugincontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

// HostManager is the seam used by Host to start supervised plugin instances.
// *pluginhost.Manager satisfies it; tests inject an in-memory fake so no real
// third-party plugin process is ever launched.
type HostManager interface {
	RegisterInstallation(pluginhost.Installation) error
	CreateInstance(pluginhost.InstanceSpec) (pluginhost.Instance, error)
	Start(tenant, id string) error
	// Disable 停用一个实例但保留其定义（期望态 enabled=false 时调用）。
	Disable(tenant, id string) error
	// Remove 删除一个实例；默认保留插件数据，只有显式 purge 才删。
	Remove(tenant, id string, opts ...pluginhost.RemoveOption) (pluginhost.RemoveResult, error)
	Close() error
}

// HostOptions configures the long-running plugin host.
type HostOptions struct {
	Manager    HostManager
	Store      *Store
	PluginsDir string
	LockPath   string
	Logger     *slog.Logger
	// Secrets 是本地 secret provider（§7）。nil 表示本 Edge 不提供本地明文：
	// 任何绑定 secret:// handle 的实例都会 fail-closed，绝不静默跳过校验。
	Secrets SecretResolver
}

// Host reloads installed plugins and enabled instance states into a Manager
// and runs until its context is canceled. It is the only component that turns
// desired state into observed process state.
type Host struct {
	opts HostOptions

	loadOnce sync.Once
	loadRes  LoadResult
	loadErr  error

	tenantOnce sync.Once
	tenantRes  LoadResult
	tenantErr  error
}

// LoadResult is a factual summary of one host load.
type LoadResult struct {
	Installations int
	Instances     int
	Started       int
	Idle          bool
}

// NewHost validates options and returns a Host. The manager is not started
// until Load or Run is called.
func NewHost(opts HostOptions) (*Host, error) {
	if opts.Manager == nil {
		return nil, fmt.Errorf("%w: manager is required", ErrInvalidState)
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("%w: state store is required", ErrInvalidState)
	}
	if strings.TrimSpace(opts.PluginsDir) == "" {
		return nil, fmt.Errorf("%w: plugins dir is required", ErrInvalidState)
	}
	if strings.TrimSpace(opts.LockPath) == "" {
		return nil, fmt.Errorf("%w: lock path is required", ErrInvalidState)
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Host{opts: opts}, nil
}

// Load registers every lockfile installation and starts every enabled desired
// instance across all tenants exactly once. Disabled instances are left
// stopped. It is safe to call Load repeatedly; subsequent calls return the
// cached result. This is the global mode used by `cloudpath plugin host`.
func (h *Host) Load(ctx context.Context) (LoadResult, error) {
	h.loadOnce.Do(func() {
		states, err := h.opts.Store.ListAll()
		if err != nil {
			h.loadErr = err
			return
		}
		h.loadRes, h.loadErr = h.load(ctx, states)
	})
	return h.loadRes, h.loadErr
}

// LoadTenant registers every lockfile installation but starts only the enabled
// desired instances of tenant. An empty tenant means "default" and never the
// global all-tenant set. Used by the edge's in-process driver host so one edge
// never starts another tenant's instances.
func (h *Host) LoadTenant(ctx context.Context, tenant string) (LoadResult, error) {
	h.tenantOnce.Do(func() {
		tenant = strings.TrimSpace(tenant)
		if tenant == "" {
			tenant = "default"
		}
		states, err := h.opts.Store.ListTenant(tenant)
		if err != nil {
			h.tenantErr = err
			return
		}
		h.tenantRes, h.tenantErr = h.load(ctx, states)
	})
	return h.tenantRes, h.tenantErr
}

// Run loads the configured state, waits for ctx cancellation, and then asks
// the manager to gracefully close every supervised process.
func (h *Host) Run(ctx context.Context) error {
	res, err := h.Load(ctx)
	if err != nil {
		return err
	}
	if res.Idle {
		h.opts.Logger.Info("plugin host idle: no installed plugins or enabled instances")
	} else {
		h.opts.Logger.Info("plugin host ready",
			"installations", res.Installations,
			"instances", res.Instances,
			"started", res.Started,
		)
	}
	<-ctx.Done()
	return h.opts.Manager.Close()
}

// registerInstallations 把 lockfile 里的每个安装物注册进 Manager（幂等：
// 已注册即跳过）。ApplySnapshot 每次应用期望态前都会调用它，因此新装插件
// 无需重启 Edge 就能被实例引用。
func (h *Host) registerInstallations(ctx context.Context) (int, error) {
	lock, err := registry.LoadLockFile(h.opts.LockPath)
	if err != nil {
		return 0, err
	}
	registered := 0
	for _, locked := range lock.Plugins {
		if err := ctx.Err(); err != nil {
			return registered, err
		}
		inst := pluginhost.Installation{
			PluginID: locked.ID,
			Version:  locked.Version,
			Path:     h.installationPath(locked),
			Kind:     h.installationKind(locked),
		}
		if err := h.opts.Manager.RegisterInstallation(inst); err != nil {
			if !errors.Is(err, pluginhost.ErrInstallationExists) {
				return registered, fmt.Errorf("register %s@%s: %w", locked.ID, locked.Version, err)
			}
			continue
		}
		registered++
	}
	return registered, nil
}

func (h *Host) load(ctx context.Context, states []InstanceState) (LoadResult, error) {
	res := LoadResult{Idle: true}
	registered, err := h.registerInstallations(ctx)
	if err != nil {
		return LoadResult{}, err
	}
	if registered > 0 {
		res.Installations += registered
		res.Idle = false
	}

	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return LoadResult{}, err
		}
		if !state.Enabled {
			continue
		}
		isolation, err := ParseIsolation(state.Isolation)
		if err != nil {
			return LoadResult{}, err
		}
		spec := pluginhost.InstanceSpec{
			ID:        state.InstanceID,
			Tenant:    state.Tenant,
			PluginID:  state.PluginID,
			Version:   state.Version,
			Config:    configForState(state),
			Isolation: isolation,
		}
		if _, err := h.opts.Manager.CreateInstance(spec); err != nil {
			if !errors.Is(err, pluginhost.ErrInstanceExists) {
				return LoadResult{}, fmt.Errorf("create instance %s/%s: %w", state.Tenant, state.InstanceID, err)
			}
			continue
		}
		if err := h.opts.Manager.Start(state.Tenant, state.InstanceID); err != nil {
			return LoadResult{}, fmt.Errorf("start instance %s/%s: %w", state.Tenant, state.InstanceID, err)
		}
		res.Instances++
		res.Started++
		res.Idle = false
	}
	return res, nil
}

func (h *Host) installationPath(locked registry.LockedPlugin) string {
	return filepath.Join(h.opts.PluginsDir, registry.SafePluginID(locked.ID), "assets", locked.Digest)
}

// installationKind reads the installed manifest kind so the host selects the
// right protocol client. A missing or unreadable manifest falls back to the
// Driver protocol rather than failing the whole host reload.
func (h *Host) installationKind(locked registry.LockedPlugin) pluginhost.Kind {
	manifestPath := filepath.Join(h.opts.PluginsDir, registry.SafePluginID(locked.ID), "plugin.yaml")
	manifest, err := registry.LoadManifest(manifestPath)
	if err != nil {
		h.opts.Logger.Debug("plugin manifest kind unavailable; defaulting to driver",
			"plugin_id", locked.ID, "error", err)
		return pluginhost.KindDriver
	}
	kind, err := pluginhost.ParseKind(manifest.Kind)
	if err != nil {
		h.opts.Logger.Debug("plugin manifest kind unknown; defaulting to driver",
			"plugin_id", locked.ID, "kind", manifest.Kind)
		return pluginhost.KindDriver
	}
	return kind
}

// configForState 合并期望态配置与配置路径，交给 Manager 建实例。
// 这里出现的值只可能是非敏感标量或 secret:// handle：明文永不进入本映射。
func configForState(state InstanceState) map[string]string {
	if len(state.Config) == 0 && strings.TrimSpace(state.ConfigPath) == "" {
		return nil
	}
	out := make(map[string]string, len(state.Config)+1)
	for k, v := range state.Config {
		out[k] = v
	}
	if strings.TrimSpace(state.ConfigPath) != "" {
		out["path"] = state.ConfigPath
	}
	return out
}
