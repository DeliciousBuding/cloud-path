package registry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ManifestSource is manifest bytes plus its resolution label/path.
type ManifestSource struct {
	Data []byte
	Path string
}

// ReadManifestSource reads root plugin.yaml from a local path, a GitHub
// repository URL, or an installed plugin id under plugins.d.
func ReadManifestSource(ctx context.Context, client *GitHubClient, source, pluginsDir string) (*ManifestSource, error) {
	raw := strings.TrimSpace(source)
	if raw == "" {
		return nil, fmt.Errorf("%w: source is empty", ErrUnsupportedSource)
	}

	if info, err := os.Stat(raw); err == nil {
		path := raw
		if info.IsDir() {
			path = filepath.Join(raw, "plugin.yaml")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read local plugin.yaml: %w", err)
		}
		return &ManifestSource{Data: data, Path: path}, nil
	}

	if strings.HasSuffix(strings.ToLower(raw), ".yaml") || strings.HasSuffix(strings.ToLower(raw), ".yml") {
		return nil, fmt.Errorf("%w: local manifest %s", ErrNotFound, raw)
	}

	repo, err := ResolveRepository(raw)
	if err == nil {
		if client == nil {
			client = NewGitHubClient()
		}
		data, err := client.FetchManifest(ctx, repo)
		if err != nil {
			return nil, err
		}
		return &ManifestSource{Data: data, Path: repo.URL + "/plugin.yaml"}, nil
	}

	if pluginsDir != "" {
		idPath := filepath.Join(pluginsDir, SafePluginID(raw), "plugin.yaml")
		if pathWithin(pluginsDir, idPath) {
			if data, err := os.ReadFile(idPath); err == nil {
				return &ManifestSource{Data: data, Path: idPath}, nil
			}
		}
	}

	return nil, fmt.Errorf("%w: cannot resolve %q", ErrUnsupportedSource, raw)
}

// SafePluginID turns arbitrary plugin IDs into a filesystem-safe directory name.
// It never returns ".", ".." or a path separator, so the result is always a
// single safe path component that cannot escape its parent.
func SafePluginID(id string) string {
	if id == "" || id == "." || id == ".." {
		return "plugin"
	}
	var b strings.Builder
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	result := b.String()
	if result == "" || result == "." || result == ".." {
		return "plugin"
	}
	return result
}
