package plugincontrol

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

// ErrInstanceHostMismatch reports a manifest kind that cannot run on the
// selected runtime host. Connector uses pluginhost.ErrConnectorUnsupported
// because no Connector runtime exists yet.
var ErrInstanceHostMismatch = errors.New("plugincontrol: plugin kind and instance host mismatch")

// ValidateInstanceHost applies the current runtime placement rule:
// Driver -> Edge, Application -> Server, Connector -> rejected until its
// runtime exists. The rule is intentionally independent of device semantics.
func ValidateInstanceHost(kind pluginhost.Kind, serverHosted bool) error {
	switch kind {
	case pluginhost.KindDriver:
		if serverHosted {
			return fmt.Errorf("%w: Driver requires an Edge host", ErrInstanceHostMismatch)
		}
	case pluginhost.KindApplication:
		if !serverHosted {
			return fmt.Errorf("%w: Application requires the Server host", ErrInstanceHostMismatch)
		}
	case pluginhost.KindConnector:
		return pluginhost.ErrConnectorUnsupported
	default:
		return fmt.Errorf("%w: unknown plugin kind %d", ErrInstanceHostMismatch, kind)
	}
	return nil
}

// InstalledPluginKind resolves one installed plugin's manifest kind from the
// existing lockfile + plugin.yaml installation layout. It performs no writes
// and fails closed on missing, mismatched or malformed metadata.
func InstalledPluginKind(pluginsDir, lockPath, pluginID string) (pluginhost.Kind, error) {
	if strings.TrimSpace(pluginsDir) == "" || strings.TrimSpace(lockPath) == "" || strings.TrimSpace(pluginID) == "" {
		return 0, fmt.Errorf("%w: plugins dir, lock path and plugin id are required", ErrInvalidState)
	}
	lock, err := registry.LoadLockFile(lockPath)
	if err != nil {
		return 0, err
	}
	locked, ok := lock.Find(pluginID)
	if !ok {
		return 0, fmt.Errorf("%w: plugin %s is not installed", registry.ErrNotFound, pluginID)
	}
	manifestPath := filepath.Join(pluginsDir, registry.SafePluginID(pluginID), "plugin.yaml")
	manifest, err := registry.ReadManifest(manifestPath)
	if err != nil {
		return 0, fmt.Errorf("read installed plugin %s manifest: %w", pluginID, err)
	}
	if manifest.ID != pluginID {
		return 0, fmt.Errorf("%w: manifest id %q does not match %q", registry.ErrInvalidManifest, manifest.ID, pluginID)
	}
	if locked.Version != "" && manifest.Version != locked.Version {
		return 0, fmt.Errorf("%w: manifest version %q does not match lock version %q", registry.ErrInvalidManifest, manifest.Version, locked.Version)
	}
	kind, err := pluginhost.ParseKind(manifest.Kind)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", registry.ErrInvalidManifest, err)
	}
	return kind, nil
}
