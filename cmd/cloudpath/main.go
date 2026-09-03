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
	defaultCoreVersion = "0.1.0"
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
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	source := fs.Arg(0)
	if source == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloudpath plugin inspect <id|url> [-plugins-dir DIR] [-schema PATH]")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	src, err := registry.ReadManifestSource(ctx, registry.NewGitHubClient(), source, *pluginsDir)
	if err != nil {
		return reportError(err, 1)
	}
	schema, err := os.ReadFile(*schemaPath)
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
	fmt.Printf("  schema-status:   valid\n")
	return 0
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
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	source := fs.Arg(0)
	if source == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloudpath plugin install <id|url> [-asset NAME] [-digest HASH] [-yes]")
		return 2
	}

	installer := registry.NewInstaller(*pluginsDir, *lockPath, *schemaPath, *coreVersion)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := installer.Install(ctx, registry.InstallOptions{
		Source:       source,
		Asset:        *asset,
		Digest:       *digest,
		ConfirmPerms: *yes,
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
	if err := fs.Parse(args); err != nil {
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
	if err := fs.Parse(args); err != nil {
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
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	pluginID := fs.Arg(0)
	if pluginID == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloudpath plugin update <id> [-yes] [-tenant NAME] [-instance ID]")
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := installer.Install(ctx, registry.InstallOptions{
		Source:       entry.Source,
		ConfirmPerms: *yes,
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
	if err := fs.Parse(args); err != nil {
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
	if err := fs.Parse(args); err != nil {
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
	case errors.Is(err, registry.ErrDigestUnavailable):
		return "ERR_DIGEST_UNAVAILABLE"
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
		errors.Is(err, registry.ErrDigestUnavailable),
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

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
