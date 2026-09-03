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
	Close() error
}

// HostOptions configures the long-running plugin host.
type HostOptions struct {
	Manager    HostManager
	Store      *Store
	PluginsDir string
	LockPath   string
	Logger     *slog.Logger
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

func (h *Host) load(ctx context.Context, states []InstanceState) (LoadResult, error) {
	lock, err := registry.LoadLockFile(h.opts.LockPath)
	if err != nil {
		return LoadResult{}, err
	}

	res := LoadResult{Idle: true}
	for _, locked := range lock.Plugins {
		if err := ctx.Err(); err != nil {
			return LoadResult{}, err
		}
		inst := pluginhost.Installation{
			PluginID: locked.ID,
			Version:  locked.Version,
			Path:     h.installationPath(locked),
		}
		err := h.opts.Manager.RegisterInstallation(inst)
		if err != nil {
			if !errors.Is(err, pluginhost.ErrInstallationExists) {
				return LoadResult{}, fmt.Errorf("register %s@%s: %w", locked.ID, locked.Version, err)
			}
			continue
		}
		res.Installations++
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

func configForState(state InstanceState) map[string]string {
	if strings.TrimSpace(state.ConfigPath) == "" {
		return nil
	}
	return map[string]string{"path": state.ConfigPath}
}
