package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	githubAPIBase       = "https://api.github.com"
	githubUserAgent     = "cloudpath-registry-cli"
	defaultMaxJSONBytes = 16 << 20 // cap JSON API responses at 16 MiB
	defaultMaxRetries   = 2
	maxSearchPages      = 5
	maxSearchResults    = 100
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

// GitHubClient is a small stdlib HTTP client for the GitHub REST API. BaseURL and
// HTTPClient are injectable so tests can point the client at an httptest server.
type GitHubClient struct {
	HTTPClient   *http.Client
	Token        string
	BaseURL      string // API base URL; defaults to githubAPIBase
	MaxJSONBytes int64  // response body cap for JSON endpoints
	MaxRetries   int    // retries for transient network errors
}

// NewGitHubClient returns a client using GITHUB_TOKEN or GH_TOKEN.
func NewGitHubClient() *GitHubClient {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}
	return &GitHubClient{
		HTTPClient:   &http.Client{Timeout: 45 * time.Second},
		Token:        token,
		BaseURL:      githubAPIBase,
		MaxJSONBytes: defaultMaxJSONBytes,
		MaxRetries:   defaultMaxRetries,
	}
}

func (c *GitHubClient) baseURL() string {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return githubAPIBase
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *GitHubClient) maxJSONBytes() int64 {
	if c == nil || c.MaxJSONBytes <= 0 {
		return defaultMaxJSONBytes
	}
	return c.MaxJSONBytes
}

func (c *GitHubClient) maxRetries() int {
	if c == nil || c.MaxRetries < 0 {
		return 0
	}
	return c.MaxRetries
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

// Search queries GitHub repositories with topic cloudpath-plugin plus query. It
// follows Link rel="next" pagination up to a bounded number of pages/results.
func (c *GitHubClient) Search(ctx context.Context, query string) ([]SearchResult, error) {
	if c == nil || c.HTTPClient == nil {
		c = NewGitHubClient()
	}
	q := "topic:cloudpath-plugin"
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		q += " " + trimmed
	}
	endpoint := c.baseURL() + "/search/repositories?" + url.Values{
		"q":        {q},
		"per_page": {"20"},
	}.Encode()

	var all []SearchResult
	for page := 0; page < maxSearchPages; page++ {
		var body struct {
			TotalCount int            `json:"total_count"`
			Items      []SearchResult `json:"items"`
		}
		data, header, err := c.fetch(ctx, endpoint)
		if err != nil {
			return nil, fmt.Errorf("search GitHub topic cloudpath-plugin: %w", err)
		}
		if err := json.Unmarshal(data, &body); err != nil {
			return nil, fmt.Errorf("decode GitHub search response: %w", err)
		}
		all = append(all, body.Items...)
		next := parseNextLink(header.Get("Link"))
		if next == "" || len(all) >= maxSearchResults {
			break
		}
		endpoint = next
	}
	for i := range all {
		if all[i].FullName != "" {
			all[i].Name = all[i].FullName
		}
		all[i].Description = strings.TrimSpace(all[i].Description)
	}
	return all, nil
}

// FetchManifest downloads repository root plugin.yaml through the contents API.
func (c *GitHubClient) FetchManifest(ctx context.Context, repo Repo) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/plugin.yaml",
		c.baseURL(), url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
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
		c.baseURL(), url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
	var release Release
	if err := c.getJSON(ctx, endpoint, &release); err != nil {
		return nil, fmt.Errorf("fetch latest release for %s: %w", repo.URL, err)
	}
	return &release, nil
}

// Download fetches a release asset into memory with a size guard.
func (c *GitHubClient) Download(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if c == nil || c.HTTPClient == nil {
		c = NewGitHubClient()
	}
	if limit <= 0 {
		limit = 512 << 20
	}
	if err := validateDownloadURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", githubUserAgent)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", safeURL(rawURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download %s: HTTP %d: %s", safeURL(rawURL), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", safeURL(rawURL), err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download %s exceeds %d bytes limit", safeURL(rawURL), limit)
	}
	return data, nil
}

// DownloadToFile streams rawURL to dest, computing the sha256 digest incrementally
// and enforcing limit. On any failure the destination file is removed so no half
// file is left behind. It returns the lowercase hex digest.
func (c *GitHubClient) DownloadToFile(ctx context.Context, rawURL, dest string, limit int64) (string, error) {
	if c == nil || c.HTTPClient == nil {
		c = NewGitHubClient()
	}
	if limit <= 0 {
		limit = 512 << 20
	}
	if err := validateDownloadURL(rawURL); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", githubUserAgent)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", safeURL(rawURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("download %s: HTTP %d: %s", safeURL(rawURL), resp.StatusCode, strings.TrimSpace(string(body)))
	}

	cleanup := func() {
		_ = os.Remove(dest)
	}
	f, err := os.Create(dest)
	if err != nil {
		cleanup()
		return "", fmt.Errorf("create asset temp file: %w", err)
	}
	h := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		f.Close()
		cleanup()
		return "", fmt.Errorf("write %s: %w", safeURL(rawURL), err)
	}
	if written > limit {
		f.Close()
		cleanup()
		return "", fmt.Errorf("download %s exceeds %d bytes limit", safeURL(rawURL), limit)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		cleanup()
		return "", fmt.Errorf("sync asset temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("close asset temp file: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c *GitHubClient) getJSON(ctx context.Context, endpoint string, dst any) error {
	data, _, err := c.fetch(ctx, endpoint)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode GitHub API response: %w", err)
	}
	return nil
}

// fetch performs an authenticated GET, enforces the JSON body cap, and translates
// non-2xx and rate-limit responses into errors wrapped with the right sentinel.
func (c *GitHubClient) fetch(ctx context.Context, endpoint string) ([]byte, http.Header, error) {
	if c == nil || c.HTTPClient == nil {
		c = NewGitHubClient()
	}
	if err := validateDownloadURL(endpoint); err != nil {
		return nil, nil, err
	}

	var resp *http.Response
	var err error
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", githubUserAgent)
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		resp, err = c.HTTPClient.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("GitHub API %s: %w", safeURL(endpoint), err)
		}
		// Only retry idempotent transient failures; rate-limit and 4xx fail closed.
		if resp.StatusCode == http.StatusInternalServerError ||
			resp.StatusCode == http.StatusBadGateway ||
			resp.StatusCode == http.StatusServiceUnavailable {
			resp.Body.Close()
			if attempt >= c.maxRetries() {
				return nil, nil, fmt.Errorf("GitHub API %s: HTTP %d", safeURL(endpoint), resp.StatusCode)
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(200 * time.Millisecond * time.Duration(attempt+1)):
			}
			continue
		}
		break
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		switch {
		case resp.StatusCode == http.StatusTooManyRequests,
			resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
			if retry := resp.Header.Get("Retry-After"); retry != "" {
				return nil, resp.Header, fmt.Errorf("%w: retry after %ss: %s", ErrRateLimited, retry, msg)
			}
			return nil, resp.Header, fmt.Errorf("%w: %s", ErrRateLimited, msg)
		default:
			return nil, resp.Header, fmt.Errorf("GitHub API %s: HTTP %d: %s", safeURL(endpoint), resp.StatusCode, msg)
		}
	}

	limit := c.maxJSONBytes()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.Header, fmt.Errorf("read GitHub API response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, resp.Header, fmt.Errorf("GitHub API response exceeds %d bytes limit", limit)
	}
	return data, resp.Header, nil
}

func validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid URL %q", ErrUnsafeArtifact, raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: unsupported URL scheme %q", ErrUnsafeArtifact, u.Scheme)
	}
	return nil
}

// safeURL strips credentials and token-like query parameters from a URL before
// it is echoed in an error message, so tokens/authorization are never printed.
func safeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	redactQueryKeys := map[string]bool{
		"token": true, "access_token": true, "apikey": true, "api_key": true,
		"key": true, "auth": true, "authorization": true, "sig": true,
		"signature": true, "private_token": true,
	}
	q := u.Query()
	changed := false
	for k := range q {
		if redactQueryKeys[strings.ToLower(k)] {
			q.Set(k, "REDACTED")
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func parseNextLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		if strings.Contains(part, `rel="next"`) {
			start := strings.Index(part, "<")
			end := strings.Index(part, ">")
			if start >= 0 && end > start {
				return part[start+1 : end]
			}
		}
	}
	return ""
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
