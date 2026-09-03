package plugincontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
)

// Sentinel errors returned by the plugin control plane. Callers can compare
// with errors.Is to distinguish stable failure modes without matching text.
var (
	ErrNotFound                       = errors.New("plugincontrol: not found")
	ErrInvalidState                   = errors.New("plugincontrol: invalid instance state")
	ErrPermissionConfirmationRequired = errors.New("plugincontrol: permission confirmation required")
)

// IsolationNames are the stable, persisted isolation values. They intentionally
// mirror pluginhost.Isolation.String so state files remain readable even if the
// internal numbering changes.
const (
	IsolationShared      = "shared"
	IsolationPerInstance = "per-instance"
)

// InstanceState is the durable desired state for one plugin instance. It is
// stored as one JSON document per tenant/instance id so a tenant can never see
// or mutate another tenant's state through the shared filesystem.
type InstanceState struct {
	Tenant     string `json:"tenant"`
	InstanceID string `json:"instanceId"`
	PluginID   string `json:"pluginId"`
	Version    string `json:"version"`
	ConfigPath string `json:"configPath,omitempty"`
	Enabled    bool   `json:"enabled"`
	Isolation  string `json:"isolation"`
	// Config 是 Server 期望态里的非敏感配置（control-plane-sync.md §7）。
	// 值只允许非敏感标量或 secret://<name> handle：**明文 secret 永不落盘**。
	// 保存它是为了 Edge 进程重启且仍离线时，能带着最后一个完整 applied revision
	// 的配置续跑（§8「Edge 重启 → 从本地 applied cache 启动」）。
	Config map[string]string `json:"config,omitempty"`
}

// Validate checks the persisted fields that are required for later reload.
func (s InstanceState) Validate() error {
	if !validStateSegment(s.Tenant) {
		return fmt.Errorf("%w: tenant %q", ErrInvalidState, s.Tenant)
	}
	if !validStateSegment(s.InstanceID) {
		return fmt.Errorf("%w: instance id %q", ErrInvalidState, s.InstanceID)
	}
	if !validStateSegment(s.PluginID) {
		return fmt.Errorf("%w: plugin id %q", ErrInvalidState, s.PluginID)
	}
	if strings.TrimSpace(s.Version) == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidState)
	}
	if _, err := ParseIsolation(s.Isolation); err != nil {
		return err
	}
	for k, v := range s.Config {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k, "\r\n\t") {
			return fmt.Errorf("%w: config key %q", ErrInvalidState, k)
		}
		if strings.ContainsAny(v, "\r\n\t") {
			return fmt.Errorf("%w: config value of %q must be a single-line scalar", ErrInvalidState, k)
		}
	}
	return nil
}

// ParseIsolation converts a persisted isolation name into the pluginhost value.
func ParseIsolation(value string) (pluginhost.Isolation, error) {
	switch strings.TrimSpace(value) {
	case "", IsolationShared:
		return pluginhost.IsolationShared, nil
	case IsolationPerInstance:
		return pluginhost.IsolationPerInstance, nil
	default:
		return 0, fmt.Errorf("%w: unknown isolation %q", ErrInvalidState, value)
	}
}

// FormatIsolation converts a pluginhost isolation value into its stable name.
func FormatIsolation(value pluginhost.Isolation) string {
	return value.String()
}

// Observation is the observed runtime state for one instance. The control
// plane separates desired state (what the CLI was asked to persist) from
// observed state (what a live Plugin Host actually reports).
type Observation struct {
	HostOnline bool
	State      pluginhost.State
	Health     pluginhost.Health
}

// String returns the stable, human-readable observation. A one-shot CLI
// enable is intentionally reported as STOPPED rather than HEALTHY because the
// CLI does not run a plugin process.
func (o Observation) String() string {
	if !o.HostOnline {
		return "STOPPED (host not running)"
	}
	return fmt.Sprintf("%s/%s", o.State, o.Health)
}

// Store persists instance states under Dir/<tenant>/<instance-id>.json using
// same-directory atomic renames. Every path component is validated before use
// so tenant or instance ids can never escape the state root.
type Store struct {
	Dir string
}

// NewStore returns a state store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{Dir: dir}
}

func (s *Store) stateDir(tenant string) (string, error) {
	if strings.TrimSpace(s.Dir) == "" {
		return "", fmt.Errorf("%w: state dir is empty", ErrInvalidState)
	}
	if !validStateSegment(tenant) {
		return "", fmt.Errorf("%w: tenant %q", ErrInvalidState, tenant)
	}
	return filepath.Join(s.Dir, tenant), nil
}

func (s *Store) statePath(tenant, instanceID string) (string, error) {
	dir, err := s.stateDir(tenant)
	if err != nil {
		return "", err
	}
	if !validStateSegment(instanceID) {
		return "", fmt.Errorf("%w: instance id %q", ErrInvalidState, instanceID)
	}
	return filepath.Join(dir, instanceID+".json"), nil
}

// Load reads one tenant-scoped instance state. A missing file maps to
// ErrNotFound and never leaks the underlying path.
func (s *Store) Load(tenant, instanceID string) (InstanceState, error) {
	path, err := s.statePath(tenant, instanceID)
	if err != nil {
		return InstanceState{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return InstanceState{}, fmt.Errorf("%w: %s/%s", ErrNotFound, tenant, instanceID)
	}
	if err != nil {
		return InstanceState{}, fmt.Errorf("read instance state: %w", err)
	}
	var state InstanceState
	if err := json.Unmarshal(data, &state); err != nil {
		return InstanceState{}, fmt.Errorf("parse instance state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return InstanceState{}, err
	}
	return state, nil
}

// Save writes one instance state atomically and fsyncs the temporary file.
func (s *Store) Save(state InstanceState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	if state.Isolation == "" {
		state.Isolation = IsolationShared
	}
	path, err := s.statePath(state.Tenant, state.InstanceID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal instance state: %w", err)
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data)
}

// Delete removes one instance state. Missing state is not an error.
func (s *Store) Delete(tenant, instanceID string) error {
	path, err := s.statePath(tenant, instanceID)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ListTenant returns every instance state for tenant sorted by instance id.
func (s *Store) ListTenant(tenant string) ([]InstanceState, error) {
	dir, err := s.stateDir(tenant)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list instance states: %w", err)
	}
	out := make([]InstanceState, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		state, err := s.Load(tenant, id)
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
	return out, nil
}

// ListAll returns every tenant's instance states, sorted by tenant then
// instance id. It is the reload view used by the plugin host.
func (s *Store) ListAll() ([]InstanceState, error) {
	if strings.TrimSpace(s.Dir) == "" {
		return nil, fmt.Errorf("%w: state dir is empty", ErrInvalidState)
	}
	tenants, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	var out []InstanceState
	for _, tenant := range tenants {
		if !tenant.IsDir() {
			continue
		}
		states, err := s.ListTenant(tenant.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, states...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tenant != out[j].Tenant {
			return out[i].Tenant < out[j].Tenant
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out, nil
}

func validStateSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && !strings.ContainsRune("._-", r) {
			return false
		}
	}
	return true
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plugin-state-*")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary state file: %w", err)
	}
	if err := renameReplace(tmpName, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}

func renameReplace(old, new string) error {
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		err = os.Rename(old, new)
		if err == nil {
			return nil
		}
		if runtime.GOOS != "windows" {
			return err
		}
		if !isWindowsSharingViolation(err) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

func isWindowsSharingViolation(err error) bool {
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33))
}
