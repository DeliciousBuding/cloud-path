// cloudpath is the Cloudpath plugin Registry CLI.
//
// Current scope: GitHub topic search, root plugin.yaml schema validation,
// verified local installs through plugins.d/plugins.lock, and stable error
// placeholders for A4 Plugin Host instance operations.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

const (
	defaultPluginsDir  = "plugins.d"
	defaultLockFile    = "plugins.lock"
	defaultSchemaPath  = "spec/plugin-manifest.schema.json"
	defaultCoreVersion = "0.1.0"
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
		return runPlaceholder("enable", rest)
	case "disable":
		return runPlaceholder("disable", rest)
	case "update":
		return runPlaceholder("update", rest)
	case "remove":
		return runPlaceholder("remove", rest)
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

func runPlaceholder(command string, args []string) int {
	id := ""
	if len(args) > 0 {
		id = args[0]
	}
	if id == "" {
		fmt.Fprintf(os.Stderr, "Usage: cloudpath plugin %s <id>\n", command)
		return 2
	}
	err := fmt.Errorf("%w: %s requires A4 Plugin Host runtime", registry.ErrHostRuntimeUnavailable, id)
	reportError(err, 2)
	return 2
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
  enable <id>          Enable a plugin instance (A4 Plugin Host)
  disable <id>         Disable a plugin instance (A4 Plugin Host)
  update <id>          Upgrade a plugin instance (A4 Plugin Host)
  remove <id>          Remove a plugin instance (A4 Plugin Host; data retained by default)`)
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
	case errors.Is(err, registry.ErrRateLimited):
		return "ERR_RATE_LIMITED"
	case errors.Is(err, registry.ErrUnsafeArtifact):
		return "ERR_UNSAFE_ARTIFACT"
	case errors.Is(err, registry.ErrUnsupportedSource), errors.Is(err, registry.ErrNotFound):
		return "ERR_NOT_FOUND"
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
