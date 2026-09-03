package storeport

import (
	"encoding/json"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// jsonOf 把公开 DTO 序列化为投影行承载的 JSON 文本；失败回落 "{}"/"[]"。
// 这里只处理公开 manifest 事实，绝不承载 secret 明文或本地绝对路径。
func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Status 把安装物投影行还原为 api 公开 DTO（读面/目录用）。
// 任何字段解析失败都按空值处理，绝不让损坏投影变成 500。
func (r PluginInstallationRow) Status() api.PluginInstallationStatusData {
	out := api.PluginInstallationStatusData{
		PluginID: r.PluginID, Version: r.Version, Kind: r.Kind, Protocol: r.Protocol,
		Digest: r.Digest, TrustMode: r.TrustMode, Verified: r.Verified,
		VerifiedPublisher: r.VerifiedPublisher,
	}
	if r.PermissionsJSON != "" {
		_ = json.Unmarshal([]byte(r.PermissionsJSON), &out.Permissions)
	}
	if r.ContributionsJSON != "" {
		_ = json.Unmarshal([]byte(r.ContributionsJSON), &out.Contributions)
	}
	if r.CapabilitiesJSON != "" {
		_ = json.Unmarshal([]byte(r.CapabilitiesJSON), &out.Capabilities)
	}
	return out
}

// Observed 把实例实际态投影行还原为 api 公开 DTO。
func (r PluginObservationRow) Observed() api.PluginObservedInstanceData {
	return api.PluginObservedInstanceData{
		InstanceID: r.InstanceID, PluginID: r.PluginID, Version: r.Version,
		HostOnline: r.HostOnline, State: r.State, Health: r.Health, Detail: r.Detail,
		RestartCount: r.RestartCount, LastHealthy: r.LastHealthy, MessageRate: r.MessageRate,
	}
}

// NewInstallationRow 把 api 安装物 DTO 投影为存储行（适配器/测试共用映射）。
func NewInstallationRow(tenantID int64, edgeID string, in api.PluginInstallationStatusData, reportedAt int64) PluginInstallationRow {
	return PluginInstallationRow{
		TenantID: tenantID, EdgeID: edgeID, PluginID: in.PluginID,
		Version: in.Version, Kind: in.Kind, Protocol: in.Protocol,
		Digest: in.Digest, TrustMode: in.TrustMode, Verified: in.Verified,
		VerifiedPublisher: in.VerifiedPublisher,
		PermissionsJSON:   jsonOf(in.Permissions),
		ContributionsJSON: jsonOf(in.Contributions),
		CapabilitiesJSON:  jsonOf(in.Capabilities),
		ReportedAt:        reportedAt,
	}
}

// NewObservationRow 把 api 实例实际态 DTO 投影为存储行（适配器/测试共用映射）。
func NewObservationRow(tenantID int64, edgeID string, o api.PluginObservedInstanceData, reportedAt int64) PluginObservationRow {
	return PluginObservationRow{
		TenantID: tenantID, EdgeID: edgeID, InstanceID: o.InstanceID,
		PluginID: o.PluginID, Version: o.Version, HostOnline: o.HostOnline,
		State: o.State, Health: o.Health, Detail: o.Detail,
		RestartCount: o.RestartCount, LastHealthy: o.LastHealthy,
		MessageRate: o.MessageRate, ReportedAt: reportedAt,
	}
}
