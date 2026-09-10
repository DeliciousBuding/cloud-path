package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const catalogTestYAML = `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: PluginCatalog
plugins:
  - id: io.github.example.driver
    slug: example-driver
    kind: Driver
    path: drivers/example
    tagPrefix: drivers/example
    asset: driver.bin
    archived: false
  - id: io.github.example.retired
    slug: retired-driver
    kind: Driver
    path: drivers/retired
    tagPrefix: drivers/retired
    archived: true
`

func mustParseCatalog(t *testing.T, data string) *PluginCatalog {
	t.Helper()
	catalog, err := ParsePluginCatalog([]byte(data))
	if err != nil {
		t.Fatalf("ParsePluginCatalog: %v", err)
	}
	return catalog
}

func TestPluginCatalogSelectorsAreExactAndArchivedIsExcluded(t *testing.T) {
	catalog := mustParseCatalog(t, catalogTestYAML)
	for _, selector := range []string{"example-driver", "io.github.example.driver", "drivers/example"} {
		entry, err := catalog.FindActive(selector)
		if err != nil {
			t.Fatalf("FindActive(%q): %v", selector, err)
		}
		if entry.Slug != "example-driver" {
			t.Fatalf("FindActive(%q) selected %q", selector, entry.Slug)
		}
	}
	for _, selector := range []string{"Example-Driver", "io.github.example.Driver", "drivers/Example", "retired-driver", "drivers/retired", "missing"} {
		if _, err := catalog.FindActive(selector); !errors.Is(err, ErrNotFound) {
			t.Fatalf("FindActive(%q) should fail with ErrNotFound, got %v", selector, err)
		}
	}
	if _, err := catalog.FindActive(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty selector should fail with ErrNotFound, got %v", err)
	}
	active := catalog.ActiveEntries()
	if len(active) != 1 || active[0].Slug != "example-driver" {
		t.Fatalf("ActiveEntries = %+v, want only example-driver", active)
	}
}

func TestPluginCatalogValidationFailsClosed(t *testing.T) {
	valid := func() PluginCatalog { return *mustParseCatalog(t, catalogTestYAML) }
	tests := []struct {
		name   string
		mutate func(*PluginCatalog)
	}{
		{"apiVersion", func(c *PluginCatalog) { c.APIVersion = "v1" }},
		{"kind", func(c *PluginCatalog) { c.Kind = "Other" }},
		{"empty", func(c *PluginCatalog) { c.Plugins = nil }},
		{"slug", func(c *PluginCatalog) { c.Plugins[0].Slug = "Bad Slug" }},
		{"pathTraversal", func(c *PluginCatalog) { c.Plugins[0].Path = "../escape"; c.Plugins[0].TagPrefix = c.Plugins[0].Path }},
		{"tagPrefixMismatch", func(c *PluginCatalog) { c.Plugins[0].TagPrefix = "drivers/other" }},
		{"assetTraversal", func(c *PluginCatalog) { c.Plugins[0].Asset = "nested/driver.bin" }},
		{"duplicateID", func(c *PluginCatalog) { c.Plugins[1].ID = c.Plugins[0].ID }},
		{"duplicateSlug", func(c *PluginCatalog) { c.Plugins[1].Slug = c.Plugins[0].Slug }},
		{"duplicatePath", func(c *PluginCatalog) {
			c.Plugins[1].Path = c.Plugins[0].Path
			c.Plugins[1].TagPrefix = c.Plugins[0].TagPrefix
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			catalog := valid()
			tc.mutate(&catalog)
			if err := ValidatePluginCatalog(&catalog); !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("ValidatePluginCatalog should reject with ErrInvalidCatalog, got %v", err)
			}
		})
	}

	if _, err := ParsePluginCatalog([]byte(strings.Replace(catalogTestYAML, "asset: driver.bin", "unknown: value", 1))); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("unknown catalog fields must fail closed, got %v", err)
	}
}

func TestPluginCatalogSchemaIsValidJSON(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "spec", "plugin-catalog.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("catalog schema is not valid JSON: %v", err)
	}
	if schema["$id"] == "" {
		t.Fatal("catalog schema is missing $id")
	}
}

func TestResolveManifestSourceLocalCatalogAndLegacy(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	root := t.TempDir()
	manifestPath := filepath.Join(root, "drivers", "example", "plugin.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins.yaml"), []byte(catalogTestYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	src, err := ResolveManifestSource(context.Background(), nil, root, "example-driver", "")
	if err != nil {
		t.Fatalf("resolve local catalog: %v", err)
	}
	if src.Entry == nil || src.Entry.Path != "drivers/example" || string(src.Data) != string(manifest) {
		t.Fatalf("unexpected catalog resolution: entry=%+v path=%s", src.Entry, src.Path)
	}
	if _, err := ResolveManifestSource(context.Background(), nil, root, "missing", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing selector should fail, got %v", err)
	}

	legacy := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, "plugin.yaml"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	src, err = ResolveManifestSource(context.Background(), nil, legacy, "", "")
	if err != nil {
		t.Fatalf("resolve legacy local plugin: %v", err)
	}
	if src.Entry != nil || src.Catalog != nil || string(src.Data) != string(manifest) {
		t.Fatalf("legacy resolution changed: %+v", src)
	}
	if _, err := ResolveManifestSource(context.Background(), nil, legacy, "io.github.example.driver", ""); err != nil {
		t.Fatalf("legacy id selector should remain compatible: %v", err)
	}
	if _, err := ResolveManifestSource(context.Background(), nil, legacy, "example-driver", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("catalog slug must not select a legacy source, got %v", err)
	}
}
