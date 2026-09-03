package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

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

// handleListPlugins 返回当前租户可见的已安装插件目录。catalog 未配置时返回空列表。
func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
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

// handleListPluginInstances 返回当前租户的插件实例（期望 + 观测）。catalog 未配置时返回空列表。
func (s *Server) handleListPluginInstances(w http.ResponseWriter, r *http.Request) {
	tenant := s.catalogTenant(r)
	if s.pluginCatalog == nil {
		writeJSON(w, http.StatusOK, map[string]any{"instances": []plugincatalog.InstanceView{}})
		return
	}
	views, err := s.pluginCatalog.Instances(tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "plugin catalog unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": views})
}

// handleGetPluginInstance 返回单个实例视图；跨租户/未知实例一律 404。
func (s *Server) handleGetPluginInstance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tenant := s.catalogTenant(r)
	if s.pluginCatalog == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "plugin instance not found"})
		return
	}
	view, ok, err := s.pluginCatalog.Instance(tenant, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "plugin catalog unavailable"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "plugin instance not found"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
