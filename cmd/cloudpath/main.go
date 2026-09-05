// cloudpath is the Cloudpath plugin Registry CLI.
//
// The CLI is the control plane for installed plugins: install/search/inspect
// are performed directly by the registry, while enable/disable/update/remove
// persist desired instance state and `plugin host` turns that state into
// supervised plugin processes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

const (
	defaultPluginsDir  = "plugins.d"
	defaultLockFile    = "plugins.lock"
	defaultSchemaPath  = "spec/plugin-manifest.schema.json"
	defaultCoreVersion = "0.2.11"
	defaultStateDir    = "data/plugin-state"
	defaultDataDir     = "data/plugin-data"
	defaultTenant      = "default"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printRootHelp()
		return 0
	}
	switch args[0] {
	case "plugin":
		return runPlugin(args[1:])
	case "help", "--help", "-h":
		printRootHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "cloudpath: unknown command %q\n", args[0])
		printRootHelp()
		return 2
	}
}

func runPlugin(args []string) int {
	if len(args) == 0 || hasHelp(args) {
		printPluginHelp()
		return 0
	}
	command := args[0]
	rest := args[1:]
	switch command {
	case "search":
		return runSearch(rest)
	case "inspect":
		return runInspect(rest)
	case "install":
		return runInstall(rest)
	case "enable":
		return runEnable(rest)
	case "disable":
		return runDisable(rest)
	case "update":
		return runUpdate(rest)
	case "remove":
		return runRemove(rest)
	case "host":
		return runHost(rest)
	default:
		fmt.Fprintf(os.Stderr, "cloudpath plugin: unknown command %q\n", command)
		printPluginHelp()
		return 2
	}
}

func runSearch(args []string) int {
	if len(args) == 0 || hasHelp(args) {
		fmt.Println("Usage: cloudpath plugin search <query>")
		fmt.Println("Query is appended to the required topic: cloudpath-plugin")
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	results, err := registry.NewGitHubClient().Search(ctx, strings.Join(args, " "))
	if err != nil {
		reportError(err, 1)
		return 1
	}
	fmt.Printf("%-40s %7s  %-48s %s\n", "NAME", "STARS", "DESCRIPTION", "URL")
	for _, result := range results {
		desc := strings.ReplaceAll(result.Description, "\n", " ")
		if desc == "" {
			desc = "-"
		}
		fmt.Printf("%-40s %7d  %-48s %s\n", result.Name, result.Stars, truncate(desc, 48), result.URL)
	}
	fmt.Println()
	fmt.Println("Topic hits are candidates only; install requires sha256 verification.")
	return 0
}

func runInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	pluginsDir := fs.String("plugins-dir", envOr("CLOUDPATH_PLUGINS_DIR", defaultPluginsDir), "installed plugin directory")
	schemaPath := fs.String("schema", envOr("CLOUDPATH_SCHEMA", defaultSchemaPath), "manifest JSON Schema path")
	lockPath := fs.String("lock", envOr("CLOUDPATH_LOCK", defaultLockFile), "plugins.lock path (used to report installed trust state)")
	fs.SetOutput(os.Stderr)
	if err := parseCommandFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	source := fs.Arg(0)
	if source == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloudpath plugin inspect <id|url> [-plugins-dir DIR] [-schema PATH] [-lock PATH]")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	src, err := registry.ReadManifestSource(ctx, registry.NewGitHubClient(), source, *pluginsDir)
	if err != nil {
		return reportError(err, 1)
	}
	schema, schemaSource, err := registry.LoadManifestSchema(*schemaPath)
	if err != nil {
		return reportError(fmt.Errorf("read schema %s: %w", *schemaPath, err), 1)
	}
	manifest, err := registry.ValidateManifest(src.Data, schema)
	if err != nil {
		return reportError(err, 3)
	}
	fmt.Printf("plugin.yaml: OK (%s)\n", src.Path)
	fmt.Printf("  id:              %s\n", manifest.ID)
	fmt.Printf("  version:         %s\n", manifest.Version)
	fmt.Printf("  kind:            %s\n", manifest.Kind)
	fmt.Printf("  protocol:        %d\n", manifest.Protocol)
	fmt.Printf("  core-compat:     %s\n", manifest.Compatibility.Core)
	fmt.Printf("  permissions:     %s\n", manifest.PermissionSummary())
	fmt.Printf("  schema-status:   valid (%s)\n", schemaSource)
	printInstalledTrust(*lockPath, manifest.ID)
	return 0
}

// printInstalledTrust reports the recorded trust anchor for an installed plugin.
// A missing or unreadable lockfile is not an error: inspect also works on sources
// that were never installed. Only non-secret trust labels are printed.
func printInstalledTrust(lockPath, pluginID string) {
	if strings.TrimSpace(lockPath) == "" {
		return
	}
	lock, err := registry.LoadLockFile(lockPath)
	if err != nil {
		fmt.Printf("  installed:     no (lockfile not readable)\n")
		return
	}
	entry, ok := lock.Find(pluginID)
	if !ok {
		fmt.Printf("  installed:     no\n")
		return
	}
	mode := entry.Mode
	if mode == "" {
		mode = "unrecorded"
	}
	fmt.Printf("  installed:     yes (%s)\n", entry.Version)
	fmt.Printf("  trust:         %s\n", mode)
	fmt.Printf("  verified:      %t\n", entry.Verified)
	if entry.Evidence != "" {
		fmt.Printf("  evidence:      %s\n", entry.Evidence)
	}
	if strings.TrimSpace(entry.VerifiedPublisher) != "" {
		fmt.Printf("  publisher:     %s\n", entry.VerifiedPublisher)
	}
	fmt.Printf("  source:        %s\n", entry.Source)
}

func runInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	pluginsDir := fs.String("plugins-dir", envOr("CLOUDPATH_PLUGINS_DIR", defaultPluginsDir), "plugin install directory")
	lockPath := fs.String("lock", envOr("CLOUDPATH_LOCK", defaultLockFile), "plugins.lock path")
	schemaPath := fs.String("schema", envOr("CLOUDPATH_SCHEMA", defaultSchemaPath), "manifest JSON Schema path")
	coreVersion := fs.String("core-version", envOr("CLOUDPATH_CORE_VERSION", defaultCoreVersion), "current Core version")
	asset := fs.String("asset", "", "exact Release asset name")
	digest := fs.String("digest", "", "expected sha256 hex (sha256:<hex> or sha256-<base64>)")
	yes := fs.Bool("yes", false, "confirm displayed permissions")
	trust := registerTrustFlags(fs)
	fs.SetOutput(os.Stderr)
	if err := parseCommandFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	source := fs.Arg(0)
	if source == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloudpath plugin install <id|url> [-asset NAME] [-digest HASH] [-registry-index PATH] [-allow-unreviewed] [-yes]")
		return 2
	}

	installer := registry.NewInstaller(*pluginsDir, *lockPath, *schemaPath, *coreVersion)
	if err := trust.configure(installer); err != nil {
		return reportError(err, 1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := installer.Install(ctx, registry.InstallOptions{
		Source:          source,
		Asset:           *asset,
		Digest:          *digest,
		ConfirmPerms:    *yes,
		AllowUnreviewed: trust.allowUnreviewed(),
	})
	if err != nil {
		return reportError(err, installErrorCode(err))
	}
	fmt.Printf("plugin installed: %s\n", result.Manifest.ID)
	fmt.Printf("  dir:       %s\n", result.PluginDir)
	fmt.Printf("  asset:     %s\n", result.AssetPath)
	fmt.Printf("  digest:    %s\n", result.Digest)
	fmt.Printf("  version:   %s\n", result.LockEntry.Version)
	fmt.Printf("  lock:      %s\n", *lockPath)
	fmt.Printf("  permissions: %s\n", result.Manifest.PermissionSummary())
	fmt.Printf("  schema:      %s\n", result.SchemaSource)
	printTrust(result)
	return 0
}

func runEnable(args []string) int {
	fs := flag.NewFlagSet("enable", flag.ContinueOnError)
	pluginsDir := fs.String("plugins-dir", envOr("CLOUDPATH_PLUGINS_DIR", defaultPluginsDir), "plugin install directory")
	lockPath := fs.String("lock", envOr("CLOUDPATH_LOCK", defaultLockFile), "plugins.lock path")
	schemaPath := fs.String("schema", envOr("CLOUDPATH_SCHEMA", defaultSchemaPath), "manifest JSON Schema path")
	coreVersion := fs.String("core-version", envOr("CLOUDPATH_CORE_VERSION", defaultCoreVersion), "current Core version")
	stateDir := fs.String("state-dir", envOr("CLOUDPATH_STATE_DIR", defaultStateDir), "plugin instance state directory")
	dataDir := fs.String("data-dir", envOr("CLOUDPATH_DATA_DIR", defaultDataDir), "plugin data directory")
	tenant := fs.String("tenant", envOr("CLOUDPATH_TENANT", defaultTenant), "owning tenant")
	instance := fs.String("instance", "", "instance id (defaults to the plugin id)")
	configPath := fs.String("config", "", "optional plugin config path")
	isolation := fs.String("isolation", plugincontrol.IsolationShared, "shared or per-instance")
	fs.SetOutput(os.Stderr)
	if err := parseCommandFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	pluginID := fs.Arg(0)
	if pluginID == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloudpath plugin enable <id> [-tenant NAME] [-instance ID] [-config PATH] [-isolation shared|per-instance]")
		return 2
	}
	instanceID := *instance
	if instanceID == "" {
		instanceID = registry.SafePluginID(pluginID)
	}
	iso, err := plugincontrol.ParseIsolation(*isolation)
	if err != nil {
		return reportError(err, 2)
	}

	ctrl, err := plugincontrol.NewController(plugincontrol.ControllerOptions{
		Store:       plugincontrol.NewStore(*stateDir),
		PluginsDir:  *pluginsDir,
		LockPath:    *lockPath,
		SchemaPath:  *schemaPath,
		CoreVersion: *coreVersion,
		DataDir:     *dataDir,
	})
	if err != nil {
		return reportError(err, 2)
	}
	result, err := ctrl.Enable(plugincontrol.EnableOptions{
		Tenant:     *tenant,
		InstanceID: instanceID,
		PluginID:   pluginID,
		ConfigPath: *configPath,
		Isolation:  iso,
	})
	if err != nil {
		return reportError(err, pluginErrorCode(err))
	}
	fmt.Printf("plugin enabled (desired): %s\n", result.Desired.PluginID)
	fmt.Printf("  tenant:     %s\n", result.Desired.Tenant)
	fmt.Printf("  instance:   %s\n", result.Desired.InstanceID)
	fmt.Printf("  version:    %s\n", result.Desired.Version)
	fmt.Printf("  isolation:  %s\n", result.Desired.Isolation)
	fmt.Printf("  observed:   %s\n", result.Observed.String())
	return 0
}

func runDisable(args []string) int {
	fs := flag.NewFlagSet("disable", flag.ContinueOnError)
	stateDir := fs.String("state-dir", envOr("CLOUDPATH_STATE_DIR", defaultStateDir), "plugin instance state directory")
	tenant := fs.String("tenant", envOr("CLOUDPATH_TENANT", defaultTenant), "owning tenant")
	instance := fs.String("instance", "", "instance id (defaults to the plugin id)")
	fs.SetOutput(os.Stderr)
	if err := parseCommandFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	pluginID := fs.Arg(0)
	if pluginID == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloudpath plugin disable <id> [-tenant NAME] [-instance ID]")
		return 2
	}
	instanceID := *instance
	if instanceID == "" {
		instanceID = registry.SafePluginID(pluginID)
	}
	ctrl, err := plugincontrol.NewController(plugincontrol.ControllerOptions{
		Store:       plugincontrol.NewStore(*stateDir),
		PluginsDir:  defaultPluginsDir,
		LockPath:    defaultLockFile,
		SchemaPath:  defaultSchemaPath,
		CoreVersion: defaultCoreVersion,
	})
	if err != nil {
		return reportError(err, 2)
	}
	result, err := ctrl.Disable(*tenant, instanceID)
	if err != nil {
		return reportError(err, pluginErrorCode(err))
	}
	fmt.Printf("plugin disabled (desired): %s\n", result.Desired.PluginID)
	fmt.Printf("  tenant:     %s\n", result.Desired.Tenant)
	fmt.Printf("  instance:   %s\n", result.Desired.InstanceID)
	fmt.Printf("  observed:   %s\n", result.Observed.String())
	return 0
}

func runUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	pluginsDir := fs.String("plugins-dir", envOr("CLOUDPATH_PLUGINS_DIR", defaultPluginsDir), "plugin install directory")
	lockPath := fs.String("lock", envOr("CLOUDPATH_LOCK", defaultLockFile), "plugins.lock path")
	schemaPath := fs.String("schema", envOr("CLOUDPATH_SCHEMA", defaultSchemaPath), "manifest JSON Schema path")
	coreVersion := fs.String("core-version", envOr("CLOUDPATH_CORE_VERSION", defaultCoreVersion), "current Core version")
	stateDir := fs.String("state-dir", envOr("CLOUDPATH_STATE_DIR", defaultStateDir), "plugin instance state directory")
	dataDir := fs.String("data-dir", envOr("CLOUDPATH_DATA_DIR", defaultDataDir), "plugin data directory")
	tenant := fs.String("tenant", envOr("CLOUDPATH_TENANT", defaultTenant), "owning tenant")
	instance := fs.String("instance", "", "instance id (defaults to the plugin id)")
	yes := fs.Bool("yes", false, "confirm permission expansion and permissions disclosure")
	digest := fs.String("digest", "", "expected sha256 hex of the new artifact (sha256:<hex> or sha256-<base64>)")
	trust := registerTrustFlags(fs)
	fs.SetOutput(os.Stderr)
	if err := parseCommandFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	pluginID := fs.Arg(0)
	if pluginID == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloudpath plugin update <id> [-digest HASH] [-registry-index PATH] [-allow-unreviewed] [-yes] [-tenant NAME] [-instance ID]")
		return 2
	}
	instanceID := *instance
	if instanceID == "" {
		instanceID = registry.SafePluginID(pluginID)
	}

	lock, err := registry.LoadLockFile(*lockPath)
	if err != nil {
		return reportError(err, 1)
	}
	entry, ok := lock.Find(pluginID)
	if !ok {
		return reportError(fmt.Errorf("%w: plugin %s is not installed", registry.ErrNotFound, pluginID), pluginErrorCode(registry.ErrNotFound))
	}

	installer := registry.NewInstaller(*pluginsDir, *lockPath, *schemaPath, *coreVersion)
	if err := trust.configure(installer); err != nil {
		return reportError(err, 1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// Existing activates validateUpdateTrust: an update must not downgrade a
	// verified installation to unreviewed TOFU, and must not silently change
	// source or verified publisher.
	result, err := installer.Install(ctx, registry.InstallOptions{
		Source:          entry.Source,
		Digest:          *digest,
		ConfirmPerms:    *yes,
		AllowUnreviewed: trust.allowUnreviewed(),
		Existing:        entry,
	})
	if err != nil {
		return reportError(err, installErrorCode(err))
	}

	ctrl, err := plugincontrol.NewController(plugincontrol.ControllerOptions{
		Store:       plugincontrol.NewStore(*stateDir),
		PluginsDir:  *pluginsDir,
		LockPath:    *lockPath,
		SchemaPath:  *schemaPath,
		CoreVersion: *coreVersion,
		DataDir:     *dataDir,
	})
	if err != nil {
		return reportError(err, 2)
	}
	state, err := ctrl.ApplyUpdateVersion(plugincontrol.ApplyUpdateOptions{
		Tenant:     *tenant,
		InstanceID: instanceID,
		PluginID:   pluginID,
		Version:    result.Manifest.Version,
	})
	if err != nil {
		return reportError(err, pluginErrorCode(err))
	}
	fmt.Printf("plugin updated (desired): %s\n", state.PluginID)
	fmt.Printf("  tenant:     %s\n", state.Tenant)
	fmt.Printf("  instance:   %s\n", state.InstanceID)
	fmt.Printf("  version:    %s\n", state.Version)
	fmt.Printf("  permissions: %s\n", result.Manifest.PermissionSummary())
	fmt.Printf("  observed:   STOPPED (host not running)\n")
	printTrust(result)
	return 0
}

func runRemove(args []string) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	stateDir := fs.String("state-dir", envOr("CLOUDPATH_STATE_DIR", defaultStateDir), "plugin instance state directory")
	dataDir := fs.String("data-dir", envOr("CLOUDPATH_DATA_DIR", defaultDataDir), "plugin data directory")
	tenant := fs.String("tenant", envOr("CLOUDPATH_TENANT", defaultTenant), "owning tenant")
	instance := fs.String("instance", "", "instance id (defaults to the plugin id)")
	purge := fs.Bool("purge", false, "delete plugin data (default preserves it)")
	fs.SetOutput(os.Stderr)
	if err := parseCommandFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	pluginID := fs.Arg(0)
	if pluginID == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloudpath plugin remove <id> [-tenant NAME] [-instance ID] [--purge]")
		return 2
	}
	instanceID := *instance
	if instanceID == "" {
		instanceID = registry.SafePluginID(pluginID)
	}
	ctrl, err := plugincontrol.NewController(plugincontrol.ControllerOptions{
		Store:       plugincontrol.NewStore(*stateDir),
		PluginsDir:  defaultPluginsDir,
		LockPath:    defaultLockFile,
		SchemaPath:  defaultSchemaPath,
		CoreVersion: defaultCoreVersion,
		DataDir:     *dataDir,
	})
	if err != nil {
		return reportError(err, 2)
	}
	result, err := ctrl.Remove(plugincontrol.RemoveOptions{
		Tenant:     *tenant,
		InstanceID: instanceID,
		Purge:      *purge,
	})
	if err != nil {
		return reportError(err, pluginErrorCode(err))
	}
	fmt.Printf("plugin removed: %s/%s\n", *tenant, instanceID)
	if result.Purged {
		fmt.Printf("  data:       purged (%s)\n", result.DataPath)
	} else {
		fmt.Printf("  data:       preserved (%s)\n", result.DataPath)
	}
	return 0
}

func runHost(args []string) int {
	fs := flag.NewFlagSet("host", flag.ContinueOnError)
	pluginsDir := fs.String("plugins-dir", envOr("CLOUDPATH_PLUGINS_DIR", defaultPluginsDir), "plugin install directory")
	lockPath := fs.String("lock", envOr("CLOUDPATH_LOCK", defaultLockFile), "plugins.lock path")
	stateDir := fs.String("state-dir", envOr("CLOUDPATH_STATE_DIR", defaultStateDir), "plugin instance state directory")
	handshakeTimeout := fs.Duration("handshake-timeout", 5*time.Second, "plugin handshake timeout")
	shutdownTimeout := fs.Duration("shutdown-timeout", 5*time.Second, "plugin shutdown timeout")
	maxRestarts := fs.Int("max-restarts", 3, "crash-loop restart budget per process")
	fs.SetOutput(os.Stderr)
	if err := parseCommandFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := pluginhost.NewManager(pluginhost.ManagerOptions{
		Runner:           pluginhost.ExecRunner{},
		Logger:           logger,
		Protocol:         "driver",
		ProtocolVersion:  1,
		HandshakeTimeout: *handshakeTimeout,
		ShutdownTimeout:  *shutdownTimeout,
		MaxRestarts:      *maxRestarts,
		BaseBackoff:      100 * time.Millisecond,
		MaxBackoff:       5 * time.Second,
	})
	host, err := plugincontrol.NewHost(plugincontrol.HostOptions{
		Manager:    manager,
		Store:      plugincontrol.NewStore(*stateDir),
		PluginsDir: *pluginsDir,
		LockPath:   *lockPath,
		Logger:     logger,
	})
	if err != nil {
		return reportError(err, 2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	res, err := host.Load(ctx)
	if err != nil {
		return reportError(err, 1)
	}
	if res.Idle {
		fmt.Println("plugin host: no installed plugins or enabled instances; waiting for signals")
	} else {
		fmt.Printf("plugin host: %d installation(s), %d enabled instance(s), %d started\n", res.Installations, res.Instances, res.Started)
	}

	if err := host.Run(ctx); err != nil {
		return reportError(err, 1)
	}
	fmt.Println("plugin host: stopped")
	return 0
}

func printRootHelp() {
	fmt.Println(`Cloudpath plugin tools

Usage:
  cloudpath plugin <command> [arguments]

Run "cloudpath plugin --help" for plugin commands.`)
}

func printPluginHelp() {
	fmt.Println(`Cloudpath plugin manager

Usage:
  cloudpath plugin <command> [arguments]

Commands:
  search <query>       Discover plugins by GitHub topic cloudpath-plugin
  inspect <id|url>     Validate root plugin.yaml against manifest schema
  install <id|url>     Download release asset, verify digest, write plugins.d/ and plugins.lock
  enable <id>          Persist an enabled plugin instance (desired state only)
  disable <id>         Persist a disabled plugin instance (desired state only)
  update <id>          Upgrade an installed plugin (compatibility/permission checks)
  remove <id>          Remove a plugin instance (data preserved unless --purge)
  host                 Run the long-lived Plugin Host (loads desired state, supervises processes)`)
}

// reportError prints a redacted error and returns the stable exit code.
func reportError(err error, code int) int {
	fmt.Fprintf(os.Stderr, "cloudpath: error[%s]: %v\n", errorCode(err), redactSecrets(err.Error()))
	return code
}

// redactSecrets strips any configured GitHub token and Authorization-style
// credentials from a message before it is printed.
func redactSecrets(s string) string {
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	return s
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, registry.ErrInvalidManifest):
		return "ERR_INVALID_MANIFEST"
	case errors.Is(err, registry.ErrDigestMismatch):
		return "ERR_DIGEST_MISMATCH"
	case errors.Is(err, registry.ErrInvalidDigest):
		return "ERR_INVALID_DIGEST"
	case errors.Is(err, registry.ErrDigestUnavailable):
		return "ERR_DIGEST_UNAVAILABLE"
	case errors.Is(err, registry.ErrTrustConfirmationRequired):
		return "ERR_TRUST_CONFIRMATION_REQUIRED"
	case errors.Is(err, registry.ErrRegistryBindingMismatch):
		return "ERR_REGISTRY_BINDING_MISMATCH"
	case errors.Is(err, registry.ErrAttestationFailed):
		return "ERR_ATTESTATION_FAILED"
	case errors.Is(err, registry.ErrTrustDowngrade):
		return "ERR_TRUST_DOWNGRADE"
	case errors.Is(err, registry.ErrCoreIncompatible):
		return "ERR_CORE_INCOMPATIBLE"
	case errors.Is(err, registry.ErrProtocolIncompatible):
		return "ERR_PROTOCOL_INCOMPATIBLE"
	case errors.Is(err, registry.ErrHostRuntimeUnavailable):
		return "ERR_HOST_RUNTIME_UNAVAILABLE"
	case errors.Is(err, registry.ErrPermissionConfirmationRequired):
		return "ERR_PERMISSION_CONFIRMATION_REQUIRED"
	case errors.Is(err, plugincontrol.ErrPermissionConfirmationRequired):
		return "ERR_PERMISSION_CONFIRMATION_REQUIRED"
	case errors.Is(err, registry.ErrRateLimited):
		return "ERR_RATE_LIMITED"
	case errors.Is(err, registry.ErrUnsafeArtifact):
		return "ERR_UNSAFE_ARTIFACT"
	case errors.Is(err, registry.ErrUnsupportedSource), errors.Is(err, registry.ErrNotFound),
		errors.Is(err, plugincontrol.ErrNotFound):
		return "ERR_NOT_FOUND"
	case errors.Is(err, plugincontrol.ErrInvalidState):
		return "ERR_INVALID_STATE"
	default:
		return "ERR_OPERATION_FAILED"
	}
}

func installErrorCode(err error) int {
	switch {
	case errors.Is(err, registry.ErrInvalidManifest),
		errors.Is(err, registry.ErrCoreIncompatible),
		errors.Is(err, registry.ErrProtocolIncompatible),
		errors.Is(err, registry.ErrDigestMismatch),
		errors.Is(err, registry.ErrInvalidDigest),
		errors.Is(err, registry.ErrDigestUnavailable),
		errors.Is(err, registry.ErrTrustConfirmationRequired),
		errors.Is(err, registry.ErrRegistryBindingMismatch),
		errors.Is(err, registry.ErrAttestationFailed),
		errors.Is(err, registry.ErrTrustDowngrade),
		errors.Is(err, registry.ErrPermissionConfirmationRequired):
		return 3
	default:
		return 1
	}
}

// pluginErrorCode maps control-plane failures to stable CLI exit codes. The
// value is intentionally separate from install errors so callers can script
// the difference between a failed install and a failed state transition.
func pluginErrorCode(err error) int {
	switch {
	case errors.Is(err, registry.ErrPermissionConfirmationRequired),
		errors.Is(err, plugincontrol.ErrPermissionConfirmationRequired),
		errors.Is(err, registry.ErrInvalidManifest),
		errors.Is(err, registry.ErrCoreIncompatible),
		errors.Is(err, registry.ErrProtocolIncompatible):
		return 3
	case errors.Is(err, plugincontrol.ErrInvalidState),
		errors.Is(err, plugincontrol.ErrNotFound),
		errors.Is(err, registry.ErrNotFound):
		return 2
	default:
		return 1
	}
}

func hasHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "help" {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// trustFlagSet carries the trust-anchor options shared by `plugin install` and
// `plugin update`. Trust decisions are resolved by the registry package; the CLI
// only supplies independent evidence and the explicit unreviewed opt-in.
type trustFlagSet struct {
	registryIndex       *string
	allowUnreviewedFlag *bool
}

// registerTrustFlags declares the trust flags on a command FlagSet.
func registerTrustFlags(fs *flag.FlagSet) trustFlagSet {
	return trustFlagSet{
		registryIndex:       fs.String("registry-index", envOr("CLOUDPATH_REGISTRY_INDEX", ""), "curated Registry index YAML supplying verified plugin bindings"),
		allowUnreviewedFlag: fs.Bool("allow-unreviewed", envBoolOr("CLOUDPATH_ALLOW_UNREVIEWED", false), "accept an unreviewed trust-on-first-use same-origin checksum"),
	}
}

// allowUnreviewed reports whether unreviewed trust-on-first-use was opted into.
func (t trustFlagSet) allowUnreviewed() bool {
	return t.allowUnreviewedFlag != nil && *t.allowUnreviewedFlag
}

// configure loads the curated Registry index onto the installer when one was
// requested. A malformed index fails closed before any install side effect, so a
// corrupt index can never silently degrade to unverified metadata.
func (t trustFlagSet) configure(installer *registry.Installer) error {
	if t.registryIndex == nil {
		return nil
	}
	path := strings.TrimSpace(*t.registryIndex)
	if path == "" {
		return nil
	}
	idx, err := registry.LoadRegistryIndex(path)
	if err != nil {
		return err
	}
	installer.RegistryIndex = idx
	return nil
}

// printTrust reports how the installed artifact was authenticated. It never
// prints secrets or local evidence payloads, only the non-secret trust label.
func printTrust(result *registry.InstallResult) {
	fmt.Printf("  trust:       %s\n", result.Mode)
	fmt.Printf("  verified:    %t\n", result.Verified)
	fmt.Printf("  evidence:    %s\n", result.Evidence)
	if publisher := strings.TrimSpace(result.LockEntry.VerifiedPublisher); publisher != "" {
		fmt.Printf("  publisher:   %s\n", publisher)
	}
}

// parseCommandFlags parses args while tolerating flags written after the
// positional argument. Go's flag package stops at the first non-flag argument,
// which would silently ignore flags in the documented order
// `plugin install <id|url> [-digest HASH] [-allow-unreviewed] [-yes]`. Silently
// dropping a user-supplied trust or confirmation flag is a safety problem, so
// flags are reordered ahead of the positionals before parsing.
func parseCommandFlags(fs *flag.FlagSet, args []string) error {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, isFlag := splitFlagName(arg)
		if !isFlag {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		// Only a known non-boolean flag may consume the next argument. An unknown
		// flag is left for flag.Parse to report, and a boolean flag never
		// consumes a value that belongs to the positional argument.
		f := fs.Lookup(name)
		if f == nil || isBoolFlag(f) {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return fs.Parse(append(flags, positional...))
}

// splitFlagName extracts the flag name from a -name or --name argument. It
// reports false for a bare "-", "--" or any non-flag argument, so a positional
// value that merely starts with a dash is never mistaken for a flag name.
func splitFlagName(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
		return "", false
	}
	name := strings.TrimLeft(arg, "-")
	if name == "" {
		return "", false
	}
	if eq := strings.IndexByte(name, '='); eq >= 0 {
		name = name[:eq]
	}
	return name, true
}

// isBoolFlag reports whether a declared flag takes no separate value.
func isBoolFlag(f *flag.Flag) bool {
	type boolFlag interface{ IsBoolFlag() bool }
	if bf, ok := f.Value.(boolFlag); ok {
		return bf.IsBoolFlag()
	}
	return false
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envBoolOr reads a boolean environment variable, falling back to def when the
// variable is unset or not a recognised boolean literal.
func envBoolOr(key string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
