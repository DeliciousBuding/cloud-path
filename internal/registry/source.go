package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ManifestSource is manifest bytes plus its resolution label/path. Repo,
// Catalog and Entry are populated only for remote or local catalog resolution.
type ManifestSource struct {
	Data    []byte
	Path    string
	Repo    Repo
	Catalog *PluginCatalog
	Entry   *PluginCatalogEntry
}

// ReadManifestSource preserves the original single-plugin resolution path. It
// cannot select an entry from a monorepo; callers that support catalogs must use
// ResolveManifestSource with an explicit selector.
func ReadManifestSource(ctx context.Context, client *GitHubClient, source, pluginsDir string) (*ManifestSource, error) {
	return ResolveManifestSource(ctx, client, source, "", pluginsDir)
}

// ResolveManifestSource reads a plugin manifest from a local path, a GitHub
// repository, or an installed plugin id under pluginsDir. Remote repositories
// are probed for root plugins.yaml first; a missing catalog falls back to the
// legacy root plugin.yaml contract.
func ResolveManifestSource(ctx context.Context, client *GitHubClient, source, selector, pluginsDir string) (*ManifestSource, error) {
	raw := strings.TrimSpace(source)
	if raw == "" {
		return nil, fmt.Errorf("%w: source is empty", ErrUnsupportedSource)
	}

	if info, err := os.Stat(raw); err == nil {
		if info.IsDir() {
			catalogPath := filepath.Join(raw, "plugins.yaml")
			if _, err := os.Stat(catalogPath); err == nil {
				return resolveLocalCatalog(raw, catalogPath, selector)
			}
			return readLocalPluginManifest(filepath.Join(raw, "plugin.yaml"), selector)
		}
		base := strings.ToLower(filepath.Base(raw))
		if base == "plugins.yaml" || base == "plugins.yml" {
			return resolveLocalCatalog(filepath.Dir(raw), raw, selector)
		}
		return readLocalPluginManifest(raw, selector)
	}

	if strings.HasSuffix(strings.ToLower(raw), ".yaml") || strings.HasSuffix(strings.ToLower(raw), ".yml") {
		return nil, fmt.Errorf("%w: local manifest %s", ErrNotFound, raw)
	}

	repo, repoErr := ResolveRepository(raw)
	if repoErr == nil {
		if client == nil {
			client = NewGitHubClient()
		}
		catalogData, err := client.FetchCatalog(ctx, repo)
		if err == nil {
			catalog, parseErr := ParsePluginCatalog(catalogData)
			if parseErr != nil {
				return nil, fmt.Errorf("%s plugins.yaml: %w", repo.URL, parseErr)
			}
			entry, selectErr := catalog.FindActive(selector)
			if selectErr != nil {
				return nil, selectErr
			}
			manifestPath := entry.Path + "/plugin.yaml"
			data, fetchErr := client.FetchRepositoryFile(ctx, repo, manifestPath)
			if fetchErr != nil {
				return nil, fetchErr
			}
			if err := validateCatalogManifestData(entry, data); err != nil {
				return nil, err
			}
			return &ManifestSource{
				Data:    data,
				Path:    repo.URL + "/" + manifestPath,
				Repo:    repo,
				Catalog: catalog,
				Entry:   entry,
			}, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		data, err := client.FetchManifest(ctx, repo)
		if err != nil {
			return nil, err
		}
		if err := validateLegacySelector(data, selector, repo.URL); err != nil {
			return nil, err
		}
		return &ManifestSource{Data: data, Path: repo.URL + "/plugin.yaml", Repo: repo}, nil
	}

	if pluginsDir != "" {
		idPath := filepath.Join(pluginsDir, SafePluginID(raw), "plugin.yaml")
		if pathWithin(pluginsDir, idPath) {
			if data, err := os.ReadFile(idPath); err == nil {
				if err := validateLegacySelector(data, selector, raw); err != nil {
					return nil, err
				}
				return &ManifestSource{Data: data, Path: idPath}, nil
			}
		}
	}

	return nil, fmt.Errorf("%w: cannot resolve %q", ErrUnsupportedSource, raw)
}

func resolveLocalCatalog(root, catalogPath, selector string) (*ManifestSource, error) {
	catalog, err := LoadPluginCatalog(catalogPath)
	if err != nil {
		return nil, err
	}
	entry, err := catalog.FindActive(selector)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(root, filepath.FromSlash(entry.Path), "plugin.yaml")
	if !pathWithin(root, manifestPath) {
		return nil, fmt.Errorf("%w: catalog path %q escapes %s", ErrUnsafeArtifact, entry.Path, root)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read catalog plugin manifest: %w", err)
	}
	if err := validateCatalogManifestData(entry, data); err != nil {
		return nil, err
	}
	return &ManifestSource{Data: data, Path: manifestPath, Catalog: catalog, Entry: entry}, nil
}

func validateCatalogManifestData(entry *PluginCatalogEntry, data []byte) error {
	manifest, err := ParseManifest(data)
	if err != nil {
		return err
	}
	return validateCatalogEntryManifest(entry, manifest)
}

func validateCatalogEntryManifest(entry *PluginCatalogEntry, manifest *Manifest) error {
	if entry == nil || manifest == nil {
		return fmt.Errorf("%w: catalog entry or manifest is nil", ErrInvalidCatalog)
	}
	if manifest.ID != entry.ID {
		return fmt.Errorf("%w: catalog entry %q resolved manifest id %q", ErrInvalidCatalog, entry.ID, manifest.ID)
	}
	if manifest.Kind != entry.Kind {
		return fmt.Errorf("%w: catalog entry %q kind %q resolved manifest kind %q", ErrInvalidCatalog, entry.ID, entry.Kind, manifest.Kind)
	}
	return nil
}

func readLocalPluginManifest(path, selector string) (*ManifestSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read local plugin.yaml: %w", err)
	}
	if err := validateLegacySelector(data, selector, path); err != nil {
		return nil, err
	}
	return &ManifestSource{Data: data, Path: path}, nil
}

// validateLegacySelector allows a single-plugin source to be selected by its
// manifest id while rejecting catalog-only slug/path selectors. An empty
// selector preserves the legacy install path unchanged.
func validateLegacySelector(data []byte, selector, source string) error {
	if strings.TrimSpace(selector) == "" {
		return nil
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return err
	}
	if manifest.ID != selector {
		return fmt.Errorf("%w: single-plugin source %s has no catalog selector %q (only its id is accepted)", ErrNotFound, source, selector)
	}
	return nil
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
