package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// InstallOptions controls one install operation.
type InstallOptions struct {
	Source       string
	Asset        string
	Digest       string
	ConfirmPerms bool
}

// InstallResult is the successful local installation outcome.
type InstallResult struct {
	Manifest      *Manifest
	PluginDir     string
	AssetPath     string
	Digest        string
	LockEntry     LockedPlugin
	RegistryEntry RegistryEntry
}

// Installer installs a GitHub release asset into plugins.d/ after verification.
type Installer struct {
	Client           *GitHubClient
	PluginsDir       string
	LockPath         string
	SchemaPath       string
	CoreVersion      string
	MaxDownloadBytes int64
}

// NewInstaller constructs an installer with repository-local defaults.
func NewInstaller(pluginsDir, lockPath, schemaPath, coreVersion string) *Installer {
	return &Installer{
		Client:           NewGitHubClient(),
		PluginsDir:       pluginsDir,
		LockPath:         lockPath,
		SchemaPath:       schemaPath,
		CoreVersion:      coreVersion,
		MaxDownloadBytes: 512 << 20,
	}
}

// Install resolves the source, validates manifest/version, downloads the
// selected release asset, verifies its sha256 digest, writes plugins.d/ and
// updates plugins.lock. It refuses installation when no expected digest is
// available or when compatibility.core does not cover the current Core.
func (i *Installer) Install(ctx context.Context, opts InstallOptions) (*InstallResult, error) {
	if strings.TrimSpace(opts.Source) == "" {
		return nil, fmt.Errorf("%w: source is required", ErrUnsupportedSource)
	}
	if i.Client == nil {
		i.Client = NewGitHubClient()
	}
	if i.PluginsDir == "" || i.LockPath == "" || i.SchemaPath == "" || i.CoreVersion == "" {
		return nil, errors.New("installer is missing plugins dir, lock path, schema path or core version")
	}

	repo, err := ResolveRepository(opts.Source)
	if err != nil {
		return nil, err
	}
	manifestData, err := i.Client.FetchManifest(ctx, repo)
	if err != nil {
		return nil, err
	}
	schemaData, err := os.ReadFile(i.SchemaPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest schema: %w", err)
	}
	manifest, err := ValidateManifest(manifestData, schemaData)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := CheckCoreCompatibility(manifest, i.CoreVersion); err != nil {
		return nil, err
	}
	if !opts.ConfirmPerms {
		return nil, fmt.Errorf("%w: permission disclosure must be confirmed: %s", ErrPermissionConfirmationRequired, manifest.PermissionSummary())
	}

	release, err := i.Client.GetLatestRelease(ctx, repo)
	if err != nil {
		return nil, err
	}
	asset, err := selectInstallAsset(release, opts.Asset)
	if err != nil {
		return nil, err
	}
	expected, err := i.expectedDigest(ctx, release, asset.Name, opts.Digest)
	if err != nil {
		return nil, err
	}
	assetData, err := i.Client.Download(ctx, asset.URL, i.MaxDownloadBytes)
	if err != nil {
		return nil, err
	}
	actual := SHA256Bytes(assetData)
	if !strings.EqualFold(actual, expected) {
		return nil, fmt.Errorf("%w: expected %s, got %s", ErrDigestMismatch, expected, actual)
	}

	pluginDir := filepath.Join(i.PluginsDir, SafePluginID(manifest.ID))
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return nil, fmt.Errorf("create plugin dir %s: %w", pluginDir, err)
	}
	manifestPath := filepath.Join(pluginDir, "plugin.yaml")
	if err := writeAtomic(manifestPath, manifestData); err != nil {
		return nil, fmt.Errorf("write plugin manifest: %w", err)
	}
	assetName := safeArtifactName(asset.Name)
	assetPath := filepath.Join(pluginDir, assetName)
	if err := writeAtomic(assetPath, assetData); err != nil {
		return nil, fmt.Errorf("write plugin asset: %w", err)
	}

	lock, err := LoadLockFile(i.LockPath)
	if err != nil {
		return nil, err
	}
	locked := LockedPlugin{
		ID:            manifest.ID,
		Version:       manifest.Version,
		Digest:        expected,
		Source:        repo.URL,
		Verified:      true,
		Protocol:      manifest.Protocol,
		Compatibility: manifest.Compatibility.Core,
	}
	lock.Upsert(locked)
	if err := WriteLockFile(i.LockPath, lock); err != nil {
		return nil, fmt.Errorf("write plugins.lock: %w", err)
	}

	return &InstallResult{
		Manifest:  manifest,
		PluginDir: pluginDir,
		AssetPath: assetPath,
		Digest:    expected,
		LockEntry: locked,
		RegistryEntry: RegistryEntry{
			ID:            manifest.ID,
			Version:       manifest.Version,
			Kind:          manifest.Kind,
			Source:        repo.URL,
			Digest:        expected,
			Protocol:      manifest.Protocol,
			Compatibility: manifest.Compatibility.Core,
		},
	}, nil
}

func selectInstallAsset(release *Release, requested string) (ReleaseAsset, error) {
	if release == nil {
		return ReleaseAsset{}, fmt.Errorf("%w: GitHub release is nil", ErrNotFound)
	}
	if requested != "" {
		for _, asset := range release.Assets {
			if asset.Name == requested {
				return asset, nil
			}
		}
		return ReleaseAsset{}, fmt.Errorf("%w: release asset %q", ErrNotFound, requested)
	}
	candidates := make([]ReleaseAsset, 0, len(release.Assets))
	for _, asset := range release.Assets {
		if !isMetadataAsset(asset.Name) {
			candidates = append(candidates, asset)
		}
	}
	if len(candidates) == 0 {
		return ReleaseAsset{}, fmt.Errorf("%w: release has no downloadable asset", ErrNotFound)
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	for _, asset := range candidates {
		if strings.Contains(strings.ToLower(asset.Name), platform) {
			return asset, nil
		}
	}
	for _, asset := range candidates {
		if strings.Contains(strings.ToLower(asset.Name), runtime.GOOS) {
			return asset, nil
		}
	}
	names := make([]string, 0, len(candidates))
	for _, asset := range candidates {
		names = append(names, asset.Name)
	}
	return ReleaseAsset{}, fmt.Errorf("multiple release assets, use --asset: %s", strings.Join(names, ", "))
}

func (i *Installer) expectedDigest(ctx context.Context, release *Release, assetName, explicit string) (string, error) {
	if explicit != "" {
		return NormalizeDigest(explicit)
	}
	for _, asset := range release.Assets {
		if asset.Name == assetName || !isChecksumAsset(asset.Name) {
			continue
		}
		data, err := i.Client.Download(ctx, asset.URL, i.MaxDownloadBytes)
		if err != nil {
			return "", fmt.Errorf("download checksum asset %s: %w", asset.Name, err)
		}
		if digest, ok := ParseChecksumBlob(data, assetName); ok {
			return NormalizeDigest(digest)
		}
	}
	return "", fmt.Errorf("%w: no sha256 checksum found for %q (use --digest only after an out-of-band check)", ErrDigestUnavailable, assetName)
}

func isMetadataAsset(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".sha256", ".sha256sum", ".sig", ".asc", ".txt", ".yaml", ".yml", ".json", ".xml", ".md"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return strings.Contains(lower, "checksum") || strings.Contains(lower, "digest") || strings.HasPrefix(lower, "sha256")
}

func isChecksumAsset(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "checksum") || strings.HasSuffix(lower, ".sha256") || strings.HasSuffix(lower, ".sha256sum") || strings.HasPrefix(lower, "sha256")
}

func safeArtifactName(name string) string {
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = filepath.Base(name)
	if name == "" || name == "." || name == ".." {
		return "plugin.bin"
	}
	return name
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cloudpath-install-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
