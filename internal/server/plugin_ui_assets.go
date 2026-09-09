package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

const (
	pluginUIAssetMaxBytes = 5 << 20
	pluginUIAssetCSP      = "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'none'; base-uri 'none'; frame-ancestors 'self'"
)

var errPluginUIAssetNotFound = errors.New("plugin UI asset not found")

var pluginUIAssetTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".map":   "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
}

// handlePluginUIAsset serves a package-relative asset from an installed
// Application plugin. It is intentionally fail-closed: authentication, tenant
// visibility, version binding, custom-entry declaration, path confinement,
// symlink resolution and MIME allowlisting are all checked before any bytes are
// returned. The response never reveals the local installation path.
func (s *Server) handlePluginUIAsset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", pluginUIAssetCSP)

	principal := auth.FromContext(r.Context())
	if principal == nil {
		principal = s.currentPrincipal(r)
	}
	if principal == nil && s.accountMode() {
		writeAPIError(w, r, http.StatusUnauthorized, apiErrAuthenticationRequired, "需要登录或有效令牌")
		return
	}
	tenant := ""
	if principal != nil {
		tenant = principal.TenantSlug
	}

	pluginID := strings.TrimSpace(chi.URLParam(r, "pluginID"))
	version := strings.TrimSpace(chi.URLParam(r, "version"))
	assetPath := chi.URLParam(r, "*")
	if pluginID == "" || version == "" || strings.ContainsAny(pluginID, "/\\") || strings.ContainsAny(version, "/\\") {
		writePluginUIAssetError(w, http.StatusNotFound)
		return
	}
	clean, err := cleanPluginUIAssetPath(assetPath)
	if err != nil {
		writePluginUIAssetError(w, http.StatusForbidden)
		return
	}
	mimeType, ok := pluginUIAssetTypes[strings.ToLower(path.Ext(clean))]
	if !ok {
		writePluginUIAssetError(w, http.StatusUnsupportedMediaType)
		return
	}

	// Tenant visibility is checked through the same catalog used by the public
	// API. A plugin that is not visible to the caller must look nonexistent.
	view, found, err := s.pluginCatalog.Plugin(tenant, pluginID)
	if err != nil || !found || view.Version != version {
		writePluginUIAssetError(w, http.StatusNotFound)
		return
	}
	root, manifest, err := s.localPluginUIRoot(pluginID, version)
	if err != nil || !registry.HasCustomUI(manifest) {
		writePluginUIAssetError(w, http.StatusNotFound)
		return
	}

	uiRoot := filepath.Join(root, "ui")
	resolvedPluginRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		writePluginUIAssetError(w, http.StatusNotFound)
		return
	}
	resolvedRoot, err := filepath.EvalSymlinks(uiRoot)
	if err != nil || !withinResolvedRoot(resolvedPluginRoot, resolvedRoot) {
		writePluginUIAssetError(w, http.StatusNotFound)
		return
	}
	rel := strings.TrimPrefix(clean, "ui/")
	fullPath := filepath.Join(uiRoot, filepath.FromSlash(rel))
	resolvedPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil || !withinResolvedRoot(resolvedRoot, resolvedPath) {
		writePluginUIAssetError(w, http.StatusNotFound)
		return
	}
	info, err := os.Stat(resolvedPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > pluginUIAssetMaxBytes {
		if err == nil && info != nil && info.Size() > pluginUIAssetMaxBytes {
			writePluginUIAssetError(w, http.StatusRequestEntityTooLarge)
			return
		}
		writePluginUIAssetError(w, http.StatusNotFound)
		return
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		writePluginUIAssetError(w, http.StatusNotFound)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.CopyN(w, file, pluginUIAssetMaxBytes+1)
}

func writePluginUIAssetError(w http.ResponseWriter, status int) {
	message := "plugin UI asset not found"
	if status == http.StatusForbidden {
		message = "plugin UI asset path is not allowed"
	}
	if status == http.StatusUnsupportedMediaType {
		message = "plugin UI asset type is not allowed"
	}
	if status == http.StatusRequestEntityTooLarge {
		message = "plugin UI asset is too large"
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func cleanPluginUIAssetPath(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\\:\x00") || strings.HasPrefix(raw, "/") || strings.Contains(raw, "://") {
		return "", errPluginUIAssetNotFound
	}
	clean := path.Clean(raw)
	if clean != raw || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean == "ui" || !strings.HasPrefix(clean, "ui/") {
		return "", errPluginUIAssetNotFound
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errPluginUIAssetNotFound
		}
	}
	return clean, nil
}

func (s *Server) localPluginUIRoot(pluginID, version string) (string, *registry.Manifest, error) {
	if s == nil || s.appHost == nil || !s.appHost.cfg.Enabled {
		return "", nil, errPluginUIAssetNotFound
	}
	lock, err := registry.LoadLockFile(s.appHost.cfg.LockPath)
	if err != nil {
		return "", nil, err
	}
	for _, locked := range lock.Plugins {
		if locked.ID != pluginID || locked.Version != version {
			continue
		}
		root := filepath.Join(s.appHost.cfg.PluginsDir, registry.SafePluginID(locked.ID))
		manifest, err := registry.ReadManifest(filepath.Join(root, "plugin.yaml"))
		if err != nil {
			return "", nil, err
		}
		if manifest.ID != locked.ID || manifest.Version != locked.Version || manifest.Kind != "Application" {
			return "", nil, errPluginUIAssetNotFound
		}
		return root, manifest, nil
	}
	return "", nil, errPluginUIAssetNotFound
}

func withinResolvedRoot(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
