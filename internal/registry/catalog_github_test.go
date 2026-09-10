package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func writeContentsJSON(t *testing.T, w http.ResponseWriter, data []byte) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"content":  base64.StdEncoding.EncodeToString(data),
		"encoding": "base64",
	}); err != nil {
		t.Fatalf("encode contents response: %v", err)
	}
}

func TestGetLatestPrefixedReleaseSelectsSemverAndFailsClosed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/plugins/releases", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]Release{
			{TagName: "other/v9.9.9", Name: "other"},
			{TagName: "drivers/example/v0.1.0", Name: "old"},
			{TagName: "drivers/example/v0.2.0", Name: "draft", Draft: true},
			{TagName: "drivers/example/v0.10.0", Name: "current"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := ghClient(srv)
	repo, err := ResolveRepository("example/plugins")
	if err != nil {
		t.Fatal(err)
	}
	release, err := client.GetLatestPrefixedRelease(context.Background(), repo, "drivers/example", "0.10.0")
	if err != nil {
		t.Fatalf("GetLatestPrefixedRelease: %v", err)
	}
	if release.TagName != "drivers/example/v0.10.0" {
		t.Fatalf("selected tag %q", release.TagName)
	}

	if _, err := client.GetLatestPrefixedRelease(context.Background(), repo, "drivers/example", "0.9.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tag/version mismatch must fail closed with ErrNotFound, got %v", err)
	}
	if _, err := client.GetLatestPrefixedRelease(context.Background(), repo, "drivers/missing", "0.1.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing prefix must fail with ErrNotFound, got %v", err)
	}
}

func TestExpandCatalogsKeepsTopicFallbackAndWarnsOnSingleFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/mono/contents/plugins.yaml", func(w http.ResponseWriter, r *http.Request) {
		writeContentsJSON(t, w, []byte(catalogTestYAML))
	})
	mux.HandleFunc("/repos/acme/broken/contents/plugins.yaml", func(w http.ResponseWriter, r *http.Request) {
		writeContentsJSON(t, w, []byte("apiVersion: wrong\nkind: PluginCatalog\nplugins: []\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := ghClient(srv)
	results := []SearchResult{
		{FullName: "acme/mono", Name: "acme/mono", Description: "mono", URL: "https://github.com/acme/mono"},
		{FullName: "acme/broken", Name: "acme/broken", Description: "broken", URL: "https://github.com/acme/broken"},
		{FullName: "acme/legacy", Name: "acme/legacy", Description: "legacy", URL: "https://github.com/acme/legacy"},
	}
	expanded, warnings := client.ExpandCatalogs(context.Background(), results)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Repository, "acme/broken") {
		t.Fatalf("warnings = %+v, want one broken catalog warning", warnings)
	}
	if len(expanded) != 3 {
		t.Fatalf("expanded result count = %d, want 3: %+v", len(expanded), expanded)
	}
	seen := map[string]bool{}
	for _, result := range expanded {
		seen[result.Name] = true
	}
	if !seen["acme/mono#example-driver"] {
		t.Fatalf("active catalog entry missing: %+v", seen)
	}
	if seen["acme/mono#retired-driver"] {
		t.Fatalf("archived catalog entry leaked into search: %+v", seen)
	}
	if !seen["acme/broken"] || !seen["acme/legacy"] {
		t.Fatalf("topic fallback was not preserved: %+v", seen)
	}
}
func TestEmptyPluginCatalogFailsClosedInsteadOfLegacyFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/plugins/contents/plugins.yaml", func(w http.ResponseWriter, r *http.Request) {
		writeContentsJSON(t, w, nil)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := ghClient(srv)
	if _, err := client.FetchCatalog(context.Background(), Repo{Owner: "example", Name: "plugins", URL: "https://github.com/example/plugins"}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("empty catalog must be invalid, got %v", err)
	}
	if _, err := ResolveManifestSource(context.Background(), client, "example/plugins", "", ""); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("empty catalog must not fall back to root plugin.yaml, got %v", err)
	}
}
