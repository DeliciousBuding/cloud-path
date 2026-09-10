package registry

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// PluginCatalogAPIVersion is the fixed API version for monorepo catalogs.
const PluginCatalogAPIVersion = "plugins.cloudpath.dev/v1alpha1"

var pluginCatalogSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// PluginCatalog is the root plugins.yaml contract for a monorepo containing
// multiple independently released plugins.
type PluginCatalog struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Plugins    []PluginCatalogEntry `yaml:"plugins" json:"plugins"`
}

// PluginCatalogEntry selects one plugin directory and its release namespace in
// a monorepo. Selectors are exact and case-sensitive; archived entries are
// never install or inspect candidates.
type PluginCatalogEntry struct {
	ID        string `yaml:"id" json:"id"`
	Slug      string `yaml:"slug" json:"slug"`
	Kind      string `yaml:"kind" json:"kind"`
	Path      string `yaml:"path" json:"path"`
	TagPrefix string `yaml:"tagPrefix" json:"tagPrefix"`
	Asset     string `yaml:"asset,omitempty" json:"asset,omitempty"`
	Archived  bool   `yaml:"archived" json:"archived"`
}

// ParsePluginCatalog decodes and validates plugins.yaml. Unknown fields are
// rejected so a catalog typo cannot silently change the resolved plugin.
func ParsePluginCatalog(data []byte) (*PluginCatalog, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var catalog PluginCatalog
	if err := dec.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCatalog, err)
	}
	if err := ValidatePluginCatalog(&catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

// LoadPluginCatalog reads and validates a local plugins.yaml file.
func LoadPluginCatalog(path string) (*PluginCatalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugins.yaml %s: %w", path, err)
	}
	catalog, err := ParsePluginCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("parse plugins.yaml %s: %w", path, err)
	}
	return catalog, nil
}

// ValidatePluginCatalog enforces the fixed catalog identity, entry shape,
// repository-relative paths, release namespaces, and duplicate-free selectors.
func ValidatePluginCatalog(catalog *PluginCatalog) error {
	if catalog == nil {
		return fmt.Errorf("%w: catalog is nil", ErrInvalidCatalog)
	}
	if catalog.APIVersion != PluginCatalogAPIVersion {
		return fmt.Errorf("%w: apiVersion %q, want %q", ErrInvalidCatalog, catalog.APIVersion, PluginCatalogAPIVersion)
	}
	if catalog.Kind != "PluginCatalog" {
		return fmt.Errorf("%w: kind %q, want PluginCatalog", ErrInvalidCatalog, catalog.Kind)
	}
	if len(catalog.Plugins) == 0 {
		return fmt.Errorf("%w: plugins must not be empty", ErrInvalidCatalog)
	}

	seenIDs := map[string]bool{}
	seenSlugs := map[string]bool{}
	seenPaths := map[string]bool{}
	seenTagPrefixes := map[string]bool{}
	for i := range catalog.Plugins {
		entry := &catalog.Plugins[i]
		prefix := fmt.Sprintf("plugins[%d]", i)
		if !validPluginID(entry.ID) {
			return fmt.Errorf("%w: %s.id %q is invalid", ErrInvalidCatalog, prefix, entry.ID)
		}
		if !pluginCatalogSlugPattern.MatchString(entry.Slug) {
			return fmt.Errorf("%w: %s.slug %q is not a lowercase kebab-case slug", ErrInvalidCatalog, prefix, entry.Slug)
		}
		if !isManifestKind(entry.Kind) {
			return fmt.Errorf("%w: %s.kind %q is invalid (Driver|Application|Connector)", ErrInvalidCatalog, prefix, entry.Kind)
		}
		if err := validateCatalogRepositoryPath(entry.Path, prefix+".path"); err != nil {
			return err
		}
		if err := validateCatalogRepositoryPath(entry.TagPrefix, prefix+".tagPrefix"); err != nil {
			return err
		}
		if entry.TagPrefix != entry.Path {
			return fmt.Errorf("%w: %s.tagPrefix %q must equal path %q", ErrInvalidCatalog, prefix, entry.TagPrefix, entry.Path)
		}
		if entry.Asset != "" {
			if err := validateCatalogAsset(entry.Asset, prefix+".asset"); err != nil {
				return err
			}
		}
		if seenIDs[entry.ID] {
			return fmt.Errorf("%w: %s.id %q is duplicated", ErrInvalidCatalog, prefix, entry.ID)
		}
		if seenSlugs[entry.Slug] {
			return fmt.Errorf("%w: %s.slug %q is duplicated", ErrInvalidCatalog, prefix, entry.Slug)
		}
		if seenPaths[entry.Path] {
			return fmt.Errorf("%w: %s.path %q is duplicated", ErrInvalidCatalog, prefix, entry.Path)
		}
		if seenTagPrefixes[entry.TagPrefix] {
			return fmt.Errorf("%w: %s.tagPrefix %q is duplicated", ErrInvalidCatalog, prefix, entry.TagPrefix)
		}
		seenIDs[entry.ID] = true
		seenSlugs[entry.Slug] = true
		seenPaths[entry.Path] = true
		seenTagPrefixes[entry.TagPrefix] = true
	}
	return nil
}

// FindActive returns the one non-archived entry matching selector exactly.
// selector may be the entry id, slug, or repository path.
func (c *PluginCatalog) FindActive(selector string) (*PluginCatalogEntry, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: catalog is nil", ErrNotFound)
	}
	if strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("%w: monorepo catalog requires --plugin <slug|id|path>", ErrNotFound)
	}
	for i := range c.Plugins {
		entry := &c.Plugins[i]
		if entry.ID != selector && entry.Slug != selector && entry.Path != selector {
			continue
		}
		if entry.Archived {
			return nil, fmt.Errorf("%w: catalog entry %q is archived", ErrNotFound, selector)
		}
		return entry, nil
	}
	return nil, fmt.Errorf("%w: catalog has no active entry matching --plugin %q", ErrNotFound, selector)
}

// ActiveEntries returns a snapshot of non-archived catalog entries in source
// order.
func (c *PluginCatalog) ActiveEntries() []PluginCatalogEntry {
	if c == nil {
		return nil
	}
	entries := make([]PluginCatalogEntry, 0, len(c.Plugins))
	for _, entry := range c.Plugins {
		if !entry.Archived {
			entries = append(entries, entry)
		}
	}
	return entries
}

// TagForVersion returns the exact release tag selected by this catalog entry.
func (e PluginCatalogEntry) TagForVersion(version string) string {
	return e.TagPrefix + "/v" + version
}

func validateCatalogRepositoryPath(raw, field string) error {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return fmt.Errorf("%w: %s must be a non-empty repository-relative path", ErrInvalidCatalog, field)
	}
	if strings.HasPrefix(raw, "/") || strings.HasSuffix(raw, "/") || strings.Contains(raw, "\\") || strings.ContainsRune(raw, 0) {
		return fmt.Errorf("%w: %s %q must use forward slashes and must not be absolute", ErrInvalidCatalog, field, raw)
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("%w: %s %q contains an invalid path component", ErrInvalidCatalog, field, raw)
		}
		for _, r := range segment {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-", r) {
				return fmt.Errorf("%w: %s %q contains unsupported characters", ErrInvalidCatalog, field, raw)
			}
		}
	}
	return nil
}

func validateCatalogAsset(raw, field string) error {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return fmt.Errorf("%w: %s must be a non-empty exact asset name", ErrInvalidCatalog, field)
	}
	if strings.ContainsAny(raw, "/\\") || strings.ContainsRune(raw, 0) {
		return fmt.Errorf("%w: %s %q must be a single asset name", ErrInvalidCatalog, field, raw)
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s %q contains control characters", ErrInvalidCatalog, field, raw)
		}
	}
	return nil
}
