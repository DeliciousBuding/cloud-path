package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	githubAPIBase   = "https://api.github.com"
	githubUserAgent = "cloudpath-registry-cli"
)

// Repo is a normalized GitHub repository reference.
type Repo struct {
	Owner string
	Name  string
	URL   string
}

// SearchResult is a plugin candidate from the open discovery channel.
type SearchResult struct {
	FullName    string `json:"full_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Stars       int    `json:"stargazers_count"`
	URL         string `json:"html_url"`
}

// ReleaseAsset describes one downloadable GitHub Release asset.
type ReleaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is a subset of GitHub Release metadata needed by the installer.
type Release struct {
	TagName string         `json:"tag_name"`
	Name    string         `json:"name"`
	URL     string         `json:"html_url"`
	Assets  []ReleaseAsset `json:"assets"`
}

// GitHubClient is a small stdlib HTTP client for the GitHub REST API.
type GitHubClient struct {
	HTTPClient *http.Client
	Token      string
}

// NewGitHubClient returns a client using GITHUB_TOKEN or GH_TOKEN.
func NewGitHubClient() *GitHubClient {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}
	return &GitHubClient{
		HTTPClient: &http.Client{Timeout: 45 * time.Second},
		Token:      token,
	}
}

// ResolveRepository parses an input into a GitHub owner/repo reference.
func ResolveRepository(input string) (Repo, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return Repo{}, fmt.Errorf("%w: source is empty", ErrUnsupportedSource)
	}
	var path string
	switch {
	case strings.HasPrefix(raw, "https://github.com/"):
		path = strings.TrimPrefix(raw, "https://github.com/")
	case strings.HasPrefix(raw, "http://github.com/"):
		path = strings.TrimPrefix(raw, "http://github.com/")
	case strings.HasPrefix(raw, "github.com/"):
		path = strings.TrimPrefix(raw, "github.com/")
	default:
		// Plain owner/repo is accepted for CLI ergonomics.
		if strings.Count(raw, "/") == 1 && !strings.Contains(raw, "\\") {
			path = raw
		} else {
			return Repo{}, fmt.Errorf("%w: expected https://github.com/owner/repo or owner/repo, got %q", ErrUnsupportedSource, raw)
		}
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return Repo{}, fmt.Errorf("%w: expected owner/repo, got %q", ErrUnsupportedSource, raw)
	}
	owner := strings.TrimSuffix(parts[0], ".git")
	repo := strings.TrimSuffix(parts[1], ".git")
	if owner == "" || repo == "" || !validGitPart(owner) || !validGitPart(repo) {
		return Repo{}, fmt.Errorf("%w: invalid GitHub repository %q", ErrUnsupportedSource, raw)
	}
	return Repo{Owner: owner, Name: repo, URL: "https://github.com/" + owner + "/" + repo}, nil
}

// Search queries GitHub repositories with topic cloudpath-plugin plus query.
func (c *GitHubClient) Search(ctx context.Context, query string) ([]SearchResult, error) {
	if c == nil || c.HTTPClient == nil {
		c = NewGitHubClient()
	}
	q := "topic:cloudpath-plugin"
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		q += " " + trimmed
	}
	endpoint := githubAPIBase + "/search/repositories?" + url.Values{
		"q":        {q},
		"per_page": {"20"},
	}.Encode()
	var body struct {
		Items []SearchResult `json:"items"`
	}
	if err := c.getJSON(ctx, endpoint, &body); err != nil {
		return nil, fmt.Errorf("search GitHub topic cloudpath-plugin: %w", err)
	}
	for i := range body.Items {
		if body.Items[i].FullName != "" {
			body.Items[i].Name = body.Items[i].FullName
		}
		body.Items[i].Description = strings.TrimSpace(body.Items[i].Description)
	}
	return body.Items, nil
}

// FetchManifest downloads repository root plugin.yaml through the contents API.
func (c *GitHubClient) FetchManifest(ctx context.Context, repo Repo) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/plugin.yaml",
		githubAPIBase, url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
	var body struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := c.getJSON(ctx, endpoint, &body); err != nil {
		return nil, fmt.Errorf("fetch %s plugin.yaml: %w", repo.URL, err)
	}
	if body.Content == "" {
		return nil, fmt.Errorf("%w: %s has no plugin.yaml at repository root", ErrNotFound, repo.URL)
	}
	if !strings.EqualFold(body.Encoding, "base64") {
		return nil, fmt.Errorf("unexpected plugin.yaml encoding %q", body.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("decode %s plugin.yaml: %w", repo.URL, err)
	}
	return decoded, nil
}

// GetLatestRelease returns the latest GitHub Release and its assets.
func (c *GitHubClient) GetLatestRelease(ctx context.Context, repo Repo) (*Release, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/latest",
		githubAPIBase, url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
	var release Release
	if err := c.getJSON(ctx, endpoint, &release); err != nil {
		return nil, fmt.Errorf("fetch latest release for %s: %w", repo.URL, err)
	}
	return &release, nil
}

// Download fetches a release asset with a size guard.
func (c *GitHubClient) Download(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if c == nil || c.HTTPClient == nil {
		c = NewGitHubClient()
	}
	if limit <= 0 {
		limit = 512 << 20
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", githubUserAgent)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download %s: HTTP %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rawURL, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download %s exceeds %d bytes limit", rawURL, limit)
	}
	return data, nil
}

func (c *GitHubClient) getJSON(ctx context.Context, endpoint string, dst any) error {
	if c == nil || c.HTTPClient == nil {
		c = NewGitHubClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", githubUserAgent)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GitHub API %s: HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode GitHub API response: %w", err)
	}
	return nil
}

func validGitPart(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-", r) {
			return false
		}
	}
	return true
}
