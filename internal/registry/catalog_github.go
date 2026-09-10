package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	githubReleasePageSize = 100
	maxReleasePages       = 3
	maxReleaseResults     = 300
)

// FetchRepositoryFile downloads one UTF-8 text file through the GitHub contents
// API. path must be repository-relative and is treated as an exact path, never
// as a wildcard or filesystem path.
func (c *GitHubClient) FetchRepositoryFile(ctx context.Context, repo Repo, path string) ([]byte, error) {
	if err := validateCatalogRepositoryPath(path, "contents path"); err != nil {
		return nil, err
	}
	segments := strings.Split(path, "/")
	for i := range segments {
		segments[i] = url.PathEscape(segments[i])
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s",
		c.baseURL(), url.PathEscape(repo.Owner), url.PathEscape(repo.Name), strings.Join(segments, "/"))
	var body struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := c.getJSON(ctx, endpoint, &body); err != nil {
		return nil, fmt.Errorf("fetch %s/%s: %w", repo.URL, path, err)
	}
	if strings.TrimSpace(body.Content) == "" {
		return nil, fmt.Errorf("%w: %s has no %s", ErrNotFound, repo.URL, path)
	}
	if !strings.EqualFold(body.Encoding, "base64") {
		return nil, fmt.Errorf("unexpected %s encoding %q", path, body.Encoding)
	}
	encoded := strings.NewReplacer("\n", "", "\r", "").Replace(body.Content)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode %s/%s: %w", repo.URL, path, err)
	}
	return decoded, nil
}

// FetchCatalog downloads repository root plugins.yaml through the contents API.
func (c *GitHubClient) FetchCatalog(ctx context.Context, repo Repo) ([]byte, error) {
	return c.FetchRepositoryFile(ctx, repo, "plugins.yaml")
}

// ListReleases lists repository releases in bounded pages. It is required for
// monorepos because /releases/latest cannot express a per-plugin tag namespace.
func (c *GitHubClient) ListReleases(ctx context.Context, repo Repo) ([]Release, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d",
		c.baseURL(), url.PathEscape(repo.Owner), url.PathEscape(repo.Name), githubReleasePageSize)
	var releases []Release
	for page := 0; page < maxReleasePages && len(releases) < maxReleaseResults; page++ {
		data, header, err := c.fetch(ctx, endpoint)
		if err != nil {
			return nil, fmt.Errorf("list releases for %s: %w", repo.URL, err)
		}
		var pageReleases []Release
		if err := json.Unmarshal(data, &pageReleases); err != nil {
			return nil, fmt.Errorf("decode releases for %s: %w", repo.URL, err)
		}
		releases = append(releases, pageReleases...)
		next := parseNextLink(header.Get("Link"))
		if next == "" {
			break
		}
		endpoint = next
	}
	if len(releases) > maxReleaseResults {
		releases = releases[:maxReleaseResults]
	}
	return releases, nil
}

// GetLatestPrefixedRelease selects the highest semver release whose tag starts
// with tagPrefix+"/v", then fails closed unless that tag is exactly the tag
// derived from the manifest version. This prevents a manifest from being paired
// with a different release in the same monorepo.
func (c *GitHubClient) GetLatestPrefixedRelease(ctx context.Context, repo Repo, tagPrefix, manifestVersion string) (*Release, error) {
	if _, err := parseSemver(manifestVersion); err != nil {
		return nil, fmt.Errorf("invalid manifest version %q: %w", manifestVersion, err)
	}
	if err := validateCatalogRepositoryPath(tagPrefix, "tagPrefix"); err != nil {
		return nil, err
	}
	releases, err := c.ListReleases(ctx, repo)
	if err != nil {
		return nil, err
	}
	release, err := selectPrefixedRelease(releases, tagPrefix)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repo.URL, err)
	}
	wantTag := tagPrefix + "/v" + manifestVersion
	if release.TagName != wantTag {
		return nil, fmt.Errorf("%w: monorepo manifest version %s resolves to release tag %q, want %q", ErrNotFound, manifestVersion, release.TagName, wantTag)
	}
	return release, nil
}

func selectPrefixedRelease(releases []Release, tagPrefix string) (*Release, error) {
	prefix := tagPrefix + "/v"
	var best *Release
	var bestVersion releaseSemver
	for i := range releases {
		release := &releases[i]
		if release.Draft || !strings.HasPrefix(release.TagName, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(release.TagName, prefix)
		version, err := parseReleaseSemver(suffix)
		if err != nil {
			continue
		}
		if best == nil || compareReleaseSemver(version, bestVersion) > 0 {
			best = release
			bestVersion = version
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w: no semver release tagged %s/v*", ErrNotFound, tagPrefix)
	}
	return best, nil
}

type releaseSemver struct {
	major      int
	minor      int
	patch      int
	prerelease []string
	hasPre     bool
}

func parseReleaseSemver(raw string) (releaseSemver, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "v") {
		value = strings.TrimPrefix(value, "v")
	}
	core := value
	pre := ""
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	if i := strings.IndexByte(core, '-'); i >= 0 {
		pre = core[i+1:]
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return releaseSemver{}, fmt.Errorf("not a semantic version: %q", raw)
	}
	parse := func(part string) (int, error) {
		if part == "" {
			return 0, fmt.Errorf("empty numeric component")
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid numeric component %q", part)
		}
		return n, nil
	}
	major, err := parse(parts[0])
	if err != nil {
		return releaseSemver{}, err
	}
	minor, err := parse(parts[1])
	if err != nil {
		return releaseSemver{}, err
	}
	patch, err := parse(parts[2])
	if err != nil {
		return releaseSemver{}, err
	}
	out := releaseSemver{major: major, minor: minor, patch: patch}
	if pre != "" {
		out.hasPre = true
		out.prerelease = strings.Split(pre, ".")
		for _, identifier := range out.prerelease {
			if identifier == "" {
				return releaseSemver{}, fmt.Errorf("invalid prerelease %q", pre)
			}
			for _, r := range identifier {
				if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '-') {
					return releaseSemver{}, fmt.Errorf("invalid prerelease %q", pre)
				}
			}
		}
	}
	return out, nil
}

func compareReleaseSemver(a, b releaseSemver) int {
	if a.major != b.major {
		if a.major < b.major {
			return -1
		}
		return 1
	}
	if a.minor != b.minor {
		if a.minor < b.minor {
			return -1
		}
		return 1
	}
	if a.patch != b.patch {
		if a.patch < b.patch {
			return -1
		}
		return 1
	}
	if a.hasPre != b.hasPre {
		if a.hasPre {
			return -1
		}
		return 1
	}
	if !a.hasPre {
		return 0
	}
	for i := 0; i < len(a.prerelease) && i < len(b.prerelease); i++ {
		left, right := a.prerelease[i], b.prerelease[i]
		leftNum, leftErr := strconv.Atoi(left)
		rightNum, rightErr := strconv.Atoi(right)
		switch {
		case leftErr == nil && rightErr == nil:
			if leftNum != rightNum {
				if leftNum < rightNum {
					return -1
				}
				return 1
			}
		case leftErr == nil:
			return -1
		case rightErr == nil:
			return 1
		default:
			if left != right {
				if left < right {
					return -1
				}
				return 1
			}
		}
	}
	if len(a.prerelease) != len(b.prerelease) {
		if len(a.prerelease) < len(b.prerelease) {
			return -1
		}
		return 1
	}
	return 0
}
