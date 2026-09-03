package plugincontrol

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

// ControllerOptions configures the CLI-facing control plane. All paths are
// injected so tests can point the controller at a temporary tree and never
// touch real plugin installs.
type ControllerOptions struct {
	Store             *Store
	PluginsDir        string
	LockPath          string
	SchemaPath        string
	CoreVersion       string
	DataDir           string
	SupportedProtocol int
}

// Controller persists desired plugin-instance state and validates each write
// against the installation lockfile and manifest. It does not launch plugin
// processes; that is the Plugin Host's job.
type Controller struct {
	opts ControllerOptions
}

// NewController validates required options and applies defaults.
func NewController(opts ControllerOptions) (*Controller, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidState)
	}
	if strings.TrimSpace(opts.PluginsDir) == "" {
		return nil, fmt.Errorf("%w: plugins dir is required", ErrInvalidState)
	}
	if strings.TrimSpace(opts.LockPath) == "" {
		return nil, fmt.Errorf("%w: lock path is required", ErrInvalidState)
	}
	if strings.TrimSpace(opts.SchemaPath) == "" {
		return nil, fmt.Errorf("%w: schema path is required", ErrInvalidState)
	}
	if strings.TrimSpace(opts.CoreVersion) == "" {
		return nil, fmt.Errorf("%w: core version is required", ErrInvalidState)
	}
	if opts.SupportedProtocol <= 0 {
		opts.SupportedProtocol = 1
	}
	return &Controller{opts: opts}, nil
}

// EnableOptions is the input for persisting an enabled desired state.
type EnableOptions struct {
	Tenant     string
	InstanceID string
	PluginID   string
	ConfigPath string
	Isolation  pluginhost.Isolation
}

// StateResult separates the persisted desired state from the observed runtime
// state. A one-shot CLI operation has no live host attached, so Observed is
// STOPPED/UNKNOWN and HostOnline is false.
type StateResult struct {
	Desired  InstanceState
	Observed Observation
}

// Enable persists an enabled instance state after validating that the plugin
// is installed, its manifest parses, and its version/contract match the
// lockfile. It never claims the plugin is online.
func (c *Controller) Enable(opts EnableOptions) (StateResult, error) {
	if opts.Tenant == "" || opts.InstanceID == "" || opts.PluginID == "" {
		return StateResult{}, fmt.Errorf("%w: enable requires tenant, instance id and plugin id", ErrInvalidState)
	}
	_, entry, err := c.installedPlugin(opts.PluginID)
	if err != nil {
		return StateResult{}, err
	}
	isolation := FormatIsolation(opts.Isolation)
	desired := InstanceState{
		Tenant:     opts.Tenant,
		InstanceID: opts.InstanceID,
		PluginID:   opts.PluginID,
		Version:    entry.Version,
		ConfigPath: opts.ConfigPath,
		Enabled:    true,
		Isolation:  isolation,
	}
	if err := c.opts.Store.Save(desired); err != nil {
		return StateResult{}, err
	}
	return StateResult{
		Desired: desired,
		Observed: Observation{
			HostOnline: false,
			State:      pluginhost.StateStopped,
			Health:     pluginhost.HealthUnknown,
		},
	}, nil
}

// Disable persists disabled desired state for one existing instance.
func (c *Controller) Disable(tenant, instanceID string) (StateResult, error) {
	if tenant == "" || instanceID == "" {
		return StateResult{}, fmt.Errorf("%w: disable requires tenant and instance id", ErrInvalidState)
	}
	state, err := c.opts.Store.Load(tenant, instanceID)
	if err != nil {
		return StateResult{}, err
	}
	state.Enabled = false
	if err := c.opts.Store.Save(state); err != nil {
		return StateResult{}, err
	}
	return StateResult{
		Desired: state,
		Observed: Observation{
			HostOnline: false,
			State:      pluginhost.StateStopped,
			Health:     pluginhost.HealthUnknown,
		},
	}, nil
}

// RemoveOptions controls instance removal. Purge must be set explicitly to
// delete plugin data; the default preserves it.
type RemoveOptions struct {
	Tenant     string
	InstanceID string
	Purge      bool
}

// RemoveResult reports the data-disposition outcome of one Remove.
type RemoveResult struct {
	Purged        bool
	DataPreserved bool
	DataPath      string
}

// Remove deletes one instance's desired state. Plugin data is preserved by
// default and only removed when Purge is explicitly true.
func (c *Controller) Remove(opts RemoveOptions) (RemoveResult, error) {
	if opts.Tenant == "" || opts.InstanceID == "" {
		return RemoveResult{}, fmt.Errorf("%w: remove requires tenant and instance id", ErrInvalidState)
	}
	if _, err := c.opts.Store.Load(opts.Tenant, opts.InstanceID); err != nil {
		return RemoveResult{}, err
	}
	if err := c.opts.Store.Delete(opts.Tenant, opts.InstanceID); err != nil {
		return RemoveResult{}, err
	}

	dataPath := c.dataPath(opts.Tenant, opts.InstanceID)
	if opts.Purge {
		if dataPath == "" {
			return RemoveResult{}, fmt.Errorf("%w: purge requested but data dir is not configured", ErrInvalidState)
		}
		if err := os.RemoveAll(dataPath); err != nil {
			return RemoveResult{}, fmt.Errorf("purge plugin data %s: %w", dataPath, err)
		}
		return RemoveResult{Purged: true, DataPreserved: false, DataPath: dataPath}, nil
	}
	return RemoveResult{Purged: false, DataPreserved: true, DataPath: dataPath}, nil
}

// UpdateCheck is the pre-install gate for upgrades: it validates the incoming
// manifest contract and rejects permission expansion unless confirmed.
// It performs no state or lockfile mutation.
func (c *Controller) UpdateCheck(pluginID string, incoming *registry.Manifest, confirm bool) ([]string, error) {
	if pluginID == "" || incoming == nil {
		return nil, fmt.Errorf("%w: update check requires plugin id and incoming manifest", ErrInvalidState)
	}
	existing, err := registry.LoadManifest(c.manifestPath(pluginID))
	if err != nil {
		return nil, fmt.Errorf("%w: installed plugin %s: %v", registry.ErrNotFound, pluginID, err)
	}
	if existing.ID != pluginID {
		return nil, fmt.Errorf("%w: plugin id mismatch %q vs %q", registry.ErrInvalidManifest, existing.ID, pluginID)
	}
	if err := registry.ValidateManifestContract(incoming, c.opts.CoreVersion, c.opts.SupportedProtocol); err != nil {
		return nil, err
	}
	added := registry.PermissionExpansion(&existing.Permissions, &incoming.Permissions)
	if len(added) > 0 && !confirm {
		return added, fmt.Errorf("%w: permissions would expand: %s", ErrPermissionConfirmationRequired, strings.Join(added, ", "))
	}
	return added, nil
}

// ApplyUpdateOptions selects the instance whose desired version is advanced
// after a successful install/update.
type ApplyUpdateOptions struct {
	Tenant     string
	InstanceID string
	PluginID   string
	Version    string
}

// ApplyUpdateVersion advances one instance's desired version after the
// installer has already validated compatibility/permissions and written the
// new lockfile/manifest.
func (c *Controller) ApplyUpdateVersion(opts ApplyUpdateOptions) (InstanceState, error) {
	if opts.Tenant == "" || opts.InstanceID == "" || opts.PluginID == "" || opts.Version == "" {
		return InstanceState{}, fmt.Errorf("%w: apply update requires tenant, instance id, plugin id and version", ErrInvalidState)
	}
	entry, manifest, err := c.installedPlugin(opts.PluginID)
	if err != nil {
		return InstanceState{}, err
	}
	if entry.Version != opts.Version {
		return InstanceState{}, fmt.Errorf("%w: lockfile version %s does not match requested %s", registry.ErrInvalidManifest, entry.Version, opts.Version)
	}
	if manifest.Version != opts.Version {
		return InstanceState{}, fmt.Errorf("%w: manifest version %s does not match requested %s", registry.ErrInvalidManifest, manifest.Version, opts.Version)
	}
	state, err := c.opts.Store.Load(opts.Tenant, opts.InstanceID)
	if err != nil {
		return InstanceState{}, err
	}
	if state.PluginID != opts.PluginID {
		return InstanceState{}, fmt.Errorf("%w: %s/%s is bound to %s, not %s", ErrNotFound, opts.Tenant, opts.InstanceID, state.PluginID, opts.PluginID)
	}
	state.Version = opts.Version
	if err := c.opts.Store.Save(state); err != nil {
		return InstanceState{}, err
	}
	return state, nil
}

// installedPlugin loads the lock entry and validates the installed manifest
// against the current schema and core contract.
func (c *Controller) installedPlugin(pluginID string) (*registry.LockedPlugin, *registry.Manifest, error) {
	lock, err := registry.LoadLockFile(c.opts.LockPath)
	if err != nil {
		return nil, nil, err
	}
	entry, ok := lock.Find(pluginID)
	if !ok {
		return nil, nil, fmt.Errorf("%w: plugin %s is not installed", registry.ErrNotFound, pluginID)
	}
	manifest, err := registry.ValidateManifestFile(c.manifestPath(pluginID), c.opts.SchemaPath)
	if err != nil {
		return nil, nil, err
	}
	if manifest.ID != pluginID {
		return nil, nil, fmt.Errorf("%w: manifest id %q does not match %q", registry.ErrInvalidManifest, manifest.ID, pluginID)
	}
	if manifest.Version != entry.Version {
		return nil, nil, fmt.Errorf("%w: manifest version %s does not match lock version %s", registry.ErrInvalidManifest, manifest.Version, entry.Version)
	}
	if err := registry.ValidateManifestContract(manifest, c.opts.CoreVersion, c.opts.SupportedProtocol); err != nil {
		return nil, nil, err
	}
	return entry, manifest, nil
}

func (c *Controller) manifestPath(pluginID string) string {
	return filepath.Join(c.opts.PluginsDir, registry.SafePluginID(pluginID), "plugin.yaml")
}

func (c *Controller) dataPath(tenant, instanceID string) string {
	if strings.TrimSpace(c.opts.DataDir) == "" {
		return ""
	}
	return filepath.Join(c.opts.DataDir, tenant, instanceID)
}
