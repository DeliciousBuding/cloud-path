package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// InstallOptions controls one install operation.
type InstallOptions struct {
	Source string
	Asset  string
	// Digest is an independent, user-supplied sha256 (--digest). It is never
	// derived from a same-origin release response.
	Digest string
	// ConfirmPerms confirms the displayed permissions (fresh installs always
	// require it; upgrades require it only on permission expansion).
	ConfirmPerms bool
	// AllowUnreviewed explicitly permits trust-on-first-use from a same-origin
	// release checksum. The resulting lock entry is recorded as unverified.
	AllowUnreviewed bool
	// Existing is the previously locked plugin during an update. When set,
	// update trust invariants are enforced before any install side effect.
	Existing *LockedPlugin
}

// InstallResult is the successful local installation outcome.
type InstallResult struct {
	Manifest  *Manifest
	PluginDir string
	AssetPath string
	Digest    string
	// Mode records the trust mode used to authenticate the artifact.
	Mode TrustMode
	// Verified is true only when independent evidence authenticated the artifact.
	Verified bool
	// Evidence is a non-secret, human-readable reference to the trust evidence.
	Evidence      string
	LockEntry     LockedPlugin
	RegistryEntry RegistryEntry
}

// Installer installs a GitHub release asset into plugins.d/ after verification.
type Installer struct {
	Client            *GitHubClient
	PluginsDir        string
	LockPath          string
	SchemaPath        string
	CoreVersion       string
	SupportedProtocol int
	MaxDownloadBytes  int64
	// RegistryIndex, when set, supplies curated verified entries.
	RegistryIndex *RegistryIndex
	// Attestation, when set, authenticates artifacts via build attestations.
	// A nil value means attestation is unavailable and is never treated as
	// verified evidence.
	Attestation AttestationVerifier
}

// NewInstaller constructs an installer with repository-local defaults.
func NewInstaller(pluginsDir, lockPath, schemaPath, coreVersion string) *Installer {
	return &Installer{
		Client:            NewGitHubClient(),
		PluginsDir:        pluginsDir,
		LockPath:          lockPath,
		SchemaPath:        schemaPath,
		CoreVersion:       coreVersion,
		SupportedProtocol: 1,
		MaxDownloadBytes:  512 << 20,
	}
}

// Install resolves the source, validates manifest/version/protocol fail-closed,
// resolves a trust decision (independent digest, verified Registry entry,
// attestation, or explicitly allowed unreviewed TOFU), downloads the selected
// release asset while streaming its sha256, verifies the digest, writes
// plugins.d/ and updates plugins.lock. The asset is stored under the plugin dir
// by digest name, so a malicious asset name cannot overwrite another plugin or
// escape the plugin data root.
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
	if i.MaxDownloadBytes <= 0 {
		i.MaxDownloadBytes = 512 << 20
	}
	supported := i.SupportedProtocol
	if supported <= 0 {
		supported = 1
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
	// Fail closed on id/version/protocol/compatibility before any install side effect.
	if err := ValidateManifestContract(manifest, i.CoreVersion, supported); err != nil {
		return nil, err
	}

	pluginDir := filepath.Join(i.PluginsDir, SafePluginID(manifest.ID))
	if !pathWithin(i.PluginsDir, pluginDir) {
		return nil, fmt.Errorf("%w: plugin id %q escapes plugins dir", ErrUnsafeArtifact, manifest.ID)
	}

	if err := i.checkPermissionConfirmation(pluginDir, manifest, opts.ConfirmPerms); err != nil {
		return nil, err
	}

	release, err := i.Client.GetLatestRelease(ctx, repo)
	if err != nil {
		return nil, err
	}
	asset, err := selectInstallAsset(release, opts.Asset)
	if err != nil {
		return nil, err
	}

	plan, err := i.planTrust(ctx, release, asset, manifest, repo, opts)
	if err != nil {
		return nil, err
	}
	if opts.Existing != nil {
		if err := validateUpdateTrust(*opts.Existing, repo, plan); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(i.PluginsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create plugins dir %s: %w", i.PluginsDir, err)
	}
	tmpFile, err := os.CreateTemp(i.PluginsDir, ".cloudpath-install-*")
	if err != nil {
		return nil, fmt.Errorf("create asset temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	tmpFile.Close()

	actual, err := i.Client.DownloadToFile(ctx, asset.URL, tmpName, i.MaxDownloadBytes)
	if err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}

	mode := plan.mode
	verified := plan.verified
	evidence := plan.evidence
	expected := plan.expected

	switch mode {
	case TrustModeAttestation:
		if i.Attestation == nil {
			_ = os.Remove(tmpName)
			return nil, fmt.Errorf("%w: attestation verifier is not configured", ErrAttestationFailed)
		}
		att, err := i.Attestation.Verify(ctx, AttestationSubject{Owner: repo.Owner, Name: repo.Name, Digest: actual})
		if err != nil {
			_ = os.Remove(tmpName)
			return nil, fmt.Errorf("%w: %v", ErrAttestationFailed, err)
		}
		if att == nil {
			_ = os.Remove(tmpName)
			return nil, fmt.Errorf("%w: verifier returned no evidence", ErrAttestationFailed)
		}
		if att.Digest != "" && !strings.EqualFold(att.Digest, actual) {
			_ = os.Remove(tmpName)
			return nil, fmt.Errorf("%w: attestation covers %s but artifact is %s", ErrDigestMismatch, att.Digest, actual)
		}
		verified = true
		evidence = attestationEvidence(att)
	default:
		if expected == "" {
			_ = os.Remove(tmpName)
			return nil, fmt.Errorf("%w: no expected digest for trust mode %q", ErrDigestUnavailable, mode)
		}
		if !strings.EqualFold(actual, expected) {
			_ = os.Remove(tmpName)
			return nil, fmt.Errorf("%w: expected %s, got %s", ErrDigestMismatch, expected, actual)
		}
	}

	assetsDir := filepath.Join(pluginDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("create asset dir %s: %w", assetsDir, err)
	}
	// 资产按内容摘要命名。发布工件以 .exe 结尾（Windows 平台产物）时保留后缀：
	// Windows 的 exec LookPath 按 PATHEXT 解析，无扩展名文件无法执行（host 侧
	// installationPath 以 .exe 变体回退，精确摘要名始终优先）。
	assetFileName := actual
	if strings.HasSuffix(strings.ToLower(asset.Name), ".exe") {
		assetFileName += ".exe"
	}
	assetPath := filepath.Join(assetsDir, assetFileName)
	if !pathWithin(i.PluginsDir, assetPath) {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("%w: asset path escapes plugins dir", ErrUnsafeArtifact)
	}
	if runtime.GOOS != "windows" {
		// CreateTemp 产出 0600：unix 上必须补执行位，host 才能 exec 安装的资产。
		if err := os.Chmod(tmpName, 0o755); err != nil {
			_ = os.Remove(tmpName)
			return nil, fmt.Errorf("mark asset executable: %w", err)
		}
	}
	if err := renameReplace(tmpName, assetPath); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("install asset %s: %w", assetPath, err)
	}

	manifestPath := filepath.Join(pluginDir, "plugin.yaml")
	if err := writeAtomic(manifestPath, manifestData); err != nil {
		return nil, fmt.Errorf("write plugin manifest: %w", err)
	}

	locked := LockedPlugin{
		ID:                manifest.ID,
		Version:           manifest.Version,
		Digest:            actual,
		Source:            repo.URL,
		Verified:          verified,
		Mode:              mode,
		Evidence:          evidence,
		VerifiedPublisher: plan.publisher,
		Protocol:          manifest.Protocol,
		Compatibility:     manifest.Compatibility.Core,
	}
	// The load-update-save on plugins.lock must be one atomic critical section per
	// install root, otherwise concurrent installs sharing the lockfile race the read
	// against a rename (Windows sharing violation) or drop updates.
	if err := withLockFile(i.LockPath, func() error {
		lock, err := LoadLockFile(i.LockPath)
		if err != nil {
			return err
		}
		lock.Upsert(locked)
		return WriteLockFile(i.LockPath, lock)
	}); err != nil {
		return nil, fmt.Errorf("update plugins.lock: %w", err)
	}

	registryEntry := RegistryEntry{
		ID:            manifest.ID,
		Version:       manifest.Version,
		Kind:          manifest.Kind,
		Source:        repo.URL,
		Digest:        actual,
		Protocol:      manifest.Protocol,
		Compatibility: manifest.Compatibility.Core,
	}
	if mode == TrustModeVerifiedRegistry {
		if entry, ok := i.RegistryIndex.Find(manifest.ID); ok {
			registryEntry = *entry
		}
	}

	return &InstallResult{
		Manifest:      manifest,
		PluginDir:     pluginDir,
		AssetPath:     assetPath,
		Digest:        actual,
		Mode:          mode,
		Verified:      verified,
		Evidence:      evidence,
		LockEntry:     locked,
		RegistryEntry: registryEntry,
	}, nil
}

// planTrust resolves the trust mode and expected digest before any install side
// effect. A same-origin checksum is only ever TOFU; independent evidence is
// required for a verified result.
func (i *Installer) planTrust(ctx context.Context, release *Release, asset ReleaseAsset, manifest *Manifest, repo Repo, opts InstallOptions) (trustPlan, error) {
	if opts.Digest != "" {
		digest, err := NormalizeDigest(opts.Digest)
		if err != nil {
			return trustPlan{}, fmt.Errorf("%w: %v", ErrInvalidDigest, err)
		}
		return trustPlan{
			mode:     TrustModeExplicitDigest,
			expected: digest,
			verified: true,
			evidence: "user supplied sha256 digest",
		}, nil
	}

	if i.RegistryIndex != nil {
		if entry, ok := i.RegistryIndex.Find(manifest.ID); ok {
			if err := ValidateRegistryBinding(entry, manifest, repo.URL); err != nil {
				return trustPlan{}, err
			}
			return trustPlan{
				mode:      TrustModeVerifiedRegistry,
				expected:  entry.Digest,
				verified:  true,
				evidence:  "verified registry entry",
				publisher: entry.VerifiedPublisher,
			}, nil
		}
	}

	if i.Attestation != nil {
		return trustPlan{
			mode:     TrustModeAttestation,
			verified: false,
			evidence: "build attestation",
		}, nil
	}

	if opts.AllowUnreviewed {
		digest, err := i.sameOriginDigest(ctx, release, asset.Name)
		if err != nil {
			return trustPlan{}, err
		}
		return trustPlan{
			mode:     TrustModeUnreviewedTOFU,
			expected: digest,
			verified: false,
			evidence: "same-origin release checksum (trust-on-first-use)",
		}, nil
	}

	return trustPlan{}, fmt.Errorf("%w: no independent digest, verified Registry entry or attestation for %s@%s (re-run with --allow-unreviewed to accept an unreviewed trust-on-first-use checksum)", ErrTrustConfirmationRequired, manifest.ID, manifest.Version)
}

// sameOriginDigest resolves the sha256 for assetName from the same-origin
// release checksum assets. It is only used for unreviewed TOFU.
func (i *Installer) sameOriginDigest(ctx context.Context, release *Release, assetName string) (string, error) {
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
	return "", fmt.Errorf("%w: no sha256 checksum found for %q", ErrDigestUnavailable, assetName)
}

// checkPermissionConfirmation requires explicit confirmation. For a fresh install
// it always requires confirmation; for an upgrade it requires confirmation only
// when the incoming manifest expands the installed permissions.
func (i *Installer) checkPermissionConfirmation(pluginDir string, incoming *Manifest, confirmed bool) error {
	existing, err := LoadManifest(filepath.Join(pluginDir, "plugin.yaml"))
	if err == nil {
		added := PermissionExpansion(&existing.Permissions, &incoming.Permissions)
		if len(added) > 0 && !confirmed {
			return fmt.Errorf("%w: permissions would expand: %s", ErrPermissionConfirmationRequired, strings.Join(added, ", "))
		}
		return nil
	}
	if !confirmed {
		return fmt.Errorf("%w: permission disclosure must be confirmed: %s", ErrPermissionConfirmationRequired, incoming.PermissionSummary())
	}
	return nil
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

// writeAtomic writes data to path in the same directory using a temporary file,
// fsyncs it, and atomically renames it into place.
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return renameReplace(tmpName, path)
}

// renameReplace atomically moves old to new, replacing any existing file at new. On Windows the destination may
// briefly be locked by another concurrent writer, so it retries a few times.
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
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

// pathWithin reports whether path is root or a descendant of root.
func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}
