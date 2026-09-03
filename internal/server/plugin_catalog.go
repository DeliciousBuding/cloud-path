package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/plugincatalog"
)

// catalogTenant 返回请求身份的租户 slug；无 principal（开发/全局形态）返回空串。
func (s *Server) catalogTenant(r *http.Request) string {
	if p := auth.FromContext(r.Context()); p != nil {
		return p.TenantSlug
	}
	return ""
}

// primePluginTenant 让插件读面能看到当前请求的租户。
//
// 插件控制面按 (tenantID, slug) 懒加载（§3 契约没有「列出全部租户」的能力），
// 因此读请求必须先登记租户映射：否则一个尚未接入过 Edge、也没写过期望态的租户
// 会被读面漏掉，表现为「明明有数据却返回空」。
func (s *Server) primePluginTenant(r *http.Request) {
	if !s.plugin.enabled() {
		return
	}
	p := auth.FromContext(r.Context())
	if p == nil || p.TenantID <= 0 {
		return
	}
	if _, err := s.plugin.ensureLoaded(p.TenantID, p.TenantSlug); err != nil {
		slog.Warn("plugin: prime tenant projection", "err", err, "tenant_id", p.TenantID)
	}
}

// handleListPlugins 返回当前租户可见的已安装插件目录。catalog 未配置时返回空列表。
func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	s.primePluginTenant(r)
	tenant := s.catalogTenant(r)
	if s.pluginCatalog == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plugins": []plugincatalog.PluginView{}})
		return
	}
	views, err := s.pluginCatalog.Plugins(tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "plugin catalog unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": views})
}

// handleGetPlugin 返回单个插件视图；不存在返回 404。
func (s *Server) handleGetPlugin(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "pluginID")
	s.primePluginTenant(r)
	tenant := s.catalogTenant(r)
	if s.pluginCatalog == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "plugin not found"})
		return
	}
	view, ok, err := s.pluginCatalog.Plugin(tenant, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "plugin catalog unavailable"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "plugin not found"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleListPluginInstances GET /api/plugin-instances：返回 api.PluginInstanceListResponse
// （契约载荷）。数据源是 Server 自己的插件控制面投影——desired 来自权威存储、
// observed 来自 Edge 上报，二者永远分开呈现；未接线时返回真实空列表而非样例数据。
//
// 注：Config.PluginCatalog 只影响 /api/plugins（安装物视图）；实例视图恒以投影为准，
// 保证写 API 与读 API 是同一份事实。
func (s *Server) handleListPluginInstances(w http.ResponseWriter, r *http.Request) {
	s.primePluginTenant(r)
	tenant := s.catalogTenant(r)
	views, err := plugincatalog.InstanceViews(pluginProjection{s}, tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "plugin catalog unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, api.PluginInstanceListResponse{Instances: views})
}

// handleGetPluginInstance 返回单个实例视图；跨租户/未知实例一律 404（不泄漏存在性）。
func (s *Server) handleGetPluginInstance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.primePluginTenant(r)
	tenant := s.catalogTenant(r)
	views, err := plugincatalog.InstanceViews(pluginProjection{s}, tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "plugin catalog unavailable"})
		return
	}
	for _, v := range views {
		if v.ID == id {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error": api.PluginErrNotFound, "code": api.PluginErrNotFound,
		"message": "plugin instance not found",
	})
}
