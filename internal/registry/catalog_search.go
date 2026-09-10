package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	maxCatalogSearchRepositories = 10
	maxCatalogSearchEntries      = 200
)

// CatalogSearchWarning describes one non-fatal catalog expansion failure. The
// original topic hit is still returned so a single broken or rate-limited
// monorepo cannot make the whole search fail.
type CatalogSearchWarning struct {
	Repository string
	Err        error
}

// ExpandCatalogs replaces each bounded topic hit that exposes a valid
// plugins.yaml with its non-archived plugin entries. Missing catalogs are
// normal; malformed catalogs and network failures become warnings while the
// original repository hit remains available as a candidate.
func (c *GitHubClient) ExpandCatalogs(ctx context.Context, results []SearchResult) ([]SearchResult, []CatalogSearchWarning) {
	if len(results) == 0 {
		return nil, nil
	}
	expanded := make([]SearchResult, 0, len(results))
	var warnings []CatalogSearchWarning
	checkedRepos := 0
	limitWarningAdded := false
	for _, result := range results {
		if checkedRepos >= maxCatalogSearchRepositories || len(expanded) >= maxCatalogSearchEntries {
			expanded = append(expanded, result)
			if !limitWarningAdded {
				warnings = append(warnings, CatalogSearchWarning{
					Repository: strings.TrimSpace(result.FullName),
					Err:        fmt.Errorf("catalog expansion limit reached; remaining topic results were not expanded"),
				})
				limitWarningAdded = true
			}
			continue
		}
		repoSource := strings.TrimSpace(result.URL)
		if repoSource == "" {
			repoSource = strings.TrimSpace(result.FullName)
		}
		repo, err := ResolveRepository(repoSource)
		if err != nil {
			expanded = append(expanded, result)
			continue
		}
		checkedRepos++
		data, err := c.FetchCatalog(ctx, repo)
		if errors.Is(err, ErrNotFound) {
			expanded = append(expanded, result)
			continue
		}
		if err != nil {
			expanded = append(expanded, result)
			warnings = append(warnings, CatalogSearchWarning{Repository: repo.URL, Err: err})
			continue
		}
		catalog, err := ParsePluginCatalog(data)
		if err != nil {
			expanded = append(expanded, result)
			warnings = append(warnings, CatalogSearchWarning{Repository: repo.URL, Err: err})
			continue
		}
		active := catalog.ActiveEntries()
		if len(active) == 0 {
			expanded = append(expanded, result)
			continue
		}
		for _, entry := range active {
			if len(expanded) >= maxCatalogSearchEntries {
				if !limitWarningAdded {
					warnings = append(warnings, CatalogSearchWarning{
						Repository: repo.URL,
						Err:        fmt.Errorf("catalog expansion limit reached; remaining entries were not expanded"),
					})
					limitWarningAdded = true
				}
				break
			}
			candidate := result
			baseName := result.Name
			if strings.TrimSpace(baseName) == "" {
				baseName = result.FullName
			}
			candidate.Name = baseName + "#" + entry.Slug
			candidate.PluginID = entry.ID
			candidate.Slug = entry.Slug
			candidate.PluginPath = entry.Path
			candidate.TagPrefix = entry.TagPrefix
			candidate.Asset = entry.Asset
			candidate.Catalog = true
			description := strings.TrimSpace(result.Description)
			if description != "" {
				description += " "
			}
			description += fmt.Sprintf("[plugin %s; slug %s; path %s]", entry.ID, entry.Slug, entry.Path)
			candidate.Description = description
			expanded = append(expanded, candidate)
		}
	}
	return expanded, warnings
}
