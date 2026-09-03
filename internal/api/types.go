// Package api 定义 edge↔server↔浏览器 的共享契约：WS 消息信封与 REST DTO。
// 两侧二进制都只认这里的类型 —— 契约变更必须同时过前端 lib/types.ts。
package api

import (
	"encoding/json"

	"github.com/DeliciousBuding/cloud-path/internal/model"
)

// Version 是消息信封协议版本，不兼容变更时递增。
const Version = 1

// MsgType 枚举 WS 消息类型。
type MsgType string

const (
	MsgHello         MsgType = "hello"          // edge→server：鉴权+注册
	MsgSnapshot      MsgType = "snapshot"       // server→浏览器：连接时的全量快照
	MsgState         MsgType = "state"          // edge→server→浏览器：设备状态
	MsgEvent         MsgType = "event"          // edge→server→浏览器：设备事件
	MsgCommand       MsgType = "command"        // server→edge：命令下发
	MsgCommandAck    MsgType = "command_ack"    // edge→server→浏览器：命令回执
	MsgEdgeUp        MsgType = "edge_up"        // server→浏览器：edge 上线
	MsgEdgeDown      MsgType = "edge_down"      // server→浏览器：edge 离线
	MsgDescriptor    MsgType = "descriptor"     // edge→server→浏览器：设备 Descriptor
	MsgPluginStatus  MsgType = "plugin_status"  // edge→server：插件安装/实例实际态全量快照
	MsgPluginDesired MsgType = "plugin_desired" // server→edge：租户/edge 插件期望态全量快照
	MsgPluginAck     MsgType = "plugin_ack"     // edge→server：期望态 revision 应用结果
	MsgPing          MsgType = "ping"
	MsgPong          MsgType = "pong"
)

// Envelope 是统一 WS 消息信封。Device 格式为 "<edge_id>/<device_id>"。
type Envelope struct {
	V      int             `json:"v"`
	Type   MsgType         `json:"type"`
	Device string          `json:"device,omitempty"`
	Ts     int64           `json:"ts"` // unix 秒
	Data   json.RawMessage `json:"data,omitempty"`
}

// HelloData 是 edge 注册载荷。
type HelloData struct {
	EdgeID  string       `json:"edge_id"`
	Token   string       `json:"token,omitempty"`
	Version string       `json:"version"`
	Tenant  string       `json:"tenant,omitempty"` // 缺省 default（docs/api.md §3 P2）
	Devices []DeviceMeta `json:"devices"`
}

// DeviceMeta 描述 edge 所辖设备的静态信息。
type DeviceMeta struct {
	ID      string `json:"id"`
	Adapter string `json:"adapter"`
	Name    string `json:"name,omitempty"`
	Port    string `json:"port,omitempty"`
}

// StateData 是设备状态快照。Raw 为适配器自定义语义（时钟/槽位/漂移…）。
type StateData struct {
	Online    bool           `json:"online"`
	Raw       map[string]any `json:"raw"`
	UpdatedAt int64          `json:"updated_at"` // unix 秒
}

// EventData 是设备事件。Type 为规范化标签（BOOT/REMIND/TAKEN/TAKEN-LATE/MISSED/SYNC-OK…）。
type EventData struct {
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
}

// CommandData 是 server→edge 命令。Cmd ∈ sync|dump|trigger|open|isp|raw。
type CommandData struct {
	CommandID int64  `json:"command_id"`
	Cmd       string `json:"cmd"`
	Args      string `json:"args,omitempty"`
}

// AckData 是命令回执。Status ∈ sent|ok|failed|timeout。
type AckData struct {
	CommandID int64  `json:"command_id"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
}

// EdgeUpData 是 edge 上/下线广播载荷（edge_down 复用同一结构）。
// 与前端 lib/types.ts 的 EdgeUpData 一一对应。
type EdgeUpData struct {
	EdgeID  string   `json:"edge_id"`
	Devices []string `json:"devices"`
	Version string   `json:"version"`
}

// SnapshotData 是浏览器连接时的全量快照。
// Descriptors 随快照一并下发，给前端可靠首屏（webui store/ws.ts 宽容消费）。
type SnapshotData struct {
	Devices     []DeviceView       `json:"devices"`
	Edges       []EdgeView         `json:"edges"`
	Descriptors []model.Descriptor `json:"descriptors,omitempty"`
}

// DeviceView 是 REST/WS 的设备视图。
type DeviceView struct {
	ID        string         `json:"id"`
	EdgeID    string         `json:"edge_id"`
	Adapter   string         `json:"adapter"`
	Name      string         `json:"name,omitempty"`
	Port      string         `json:"port,omitempty"`
	Online    bool           `json:"online"`
	State     map[string]any `json:"state"`
	UpdatedAt int64          `json:"updated_at"`
	LastSeen  int64          `json:"last_seen"`
}

// EdgeView 是边缘节点视图。
type EdgeView struct {
	EdgeID      string   `json:"edge_id"`
	Online      bool     `json:"online"`
	Version     string   `json:"version"`
	Devices     []string `json:"devices"`
	ConnectedAt int64    `json:"connected_at"`
}

// EventView 是事件历史行。
type EventView struct {
	ID       int64  `json:"id"`
	DeviceID string `json:"device_id"`
	Ts       int64  `json:"ts"`
	Type     string `json:"type"`
	Payload  string `json:"payload"`
}

// CommandView 是命令行。
type CommandView struct {
	ID        int64  `json:"id"`
	DeviceID  string `json:"device_id"`
	Cmd       string `json:"cmd"`
	Args      string `json:"args"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	AckedAt   int64  `json:"acked_at"`
	Result    string `json:"result"`
}

// HealthView 是 /healthz 载荷。
type HealthView struct {
	OK            bool   `json:"ok"`
	Version       string `json:"version"`
	UptimeS       int64  `json:"uptime_s"`
	DevicesOnline int    `json:"devices_online"`
	DevicesTotal  int    `json:"devices_total"`
	EdgesOnline   int    `json:"edges_online"`
}

// DeviceKey 拼 "<edge_id>/<device_id>"。
func DeviceKey(edgeID, deviceID string) string { return edgeID + "/" + deviceID }

// AdapterView 描述一个已注册的设备适配器及其命令白名单。
// 前端命令面板以此为唯一事实源，新增适配器无需改前端。
type AdapterView struct {
	Name     string   `json:"name"`
	Commands []string `json:"commands"`
}

// StatsView 是存储/运行统计（系统页）。
type StatsView struct {
	Devices       int64 `json:"devices"`
	Events        int64 `json:"events"`
	Commands      int64 `json:"commands"`
	OldestEvent   int64 `json:"oldest_event"`
	SchemaVersion int   `json:"schema_version"`
	RetentionDays int   `json:"retention_days"`
	AuthEnabled   bool  `json:"auth_enabled"`
}

// ---- 插件控制面同步（docs/architecture/control-plane-sync.md）----

// PluginPermissionsData 是插件公开权限声明。这里只携带权限名称，永不携带凭据值。
type PluginPermissionsData struct {
	Hardware   []string `json:"hardware,omitempty"`
	Network    []string `json:"network,omitempty"`
	Filesystem []string `json:"filesystem,omitempty"`
	Secrets    []string `json:"secrets,omitempty"`
}

// PluginDriverContributionData 是 Driver 插件对外贡献的稳定公开元数据。
type PluginDriverContributionData struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Discovery string `json:"discovery,omitempty"`
}

// PluginApplicationContributionData 是 Application 插件对外贡献的稳定公开元数据。
type PluginApplicationContributionData struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// PluginConnectorContributionData 是 Connector 插件对外贡献的稳定公开元数据。
type PluginConnectorContributionData struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Direction string `json:"direction,omitempty"`
	Host      string `json:"host,omitempty"`
}

// PluginContributionsData 汇总一个安装物提供的贡献。它只含公开 manifest 字段。
type PluginContributionsData struct {
	Drivers      []PluginDriverContributionData      `json:"drivers,omitempty"`
	Applications []PluginApplicationContributionData `json:"applications,omitempty"`
	Connectors   []PluginConnectorContributionData   `json:"connectors,omitempty"`
}

// PluginInstallationStatusData 是 Edge 上报的已安装插件公开事实。
// 禁止添加本地路径、启动参数、环境变量或 secret 值。
type PluginInstallationStatusData struct {
	PluginID          string                  `json:"plugin_id"`
	Version           string                  `json:"version"`
	Kind              string                  `json:"kind"`
	Protocol          int                     `json:"protocol"`
	Digest            string                  `json:"digest"`
	TrustMode         string                  `json:"trust_mode"`
	Verified          bool                    `json:"verified"`
	VerifiedPublisher string                  `json:"verified_publisher,omitempty"`
	Permissions       PluginPermissionsData   `json:"permissions"`
	Contributions     PluginContributionsData `json:"contributions"`
	Capabilities      []string                `json:"capabilities,omitempty"`
}

// PluginObservedInstanceData 是 Edge Plugin Host 的实际态。desired 字段不得放进本结构。
type PluginObservedInstanceData struct {
	InstanceID   string  `json:"instance_id"`
	PluginID     string  `json:"plugin_id"`
	Version      string  `json:"version"`
	HostOnline   bool    `json:"host_online"`
	State        string  `json:"state"`
	Health       string  `json:"health"`
	Detail       string  `json:"detail,omitempty"`
	RestartCount int     `json:"restart_count"`
	LastHealthy  int64   `json:"last_healthy,omitempty"`
	MessageRate  float64 `json:"message_rate,omitempty"`
}

// PluginStatusData 是 Edge→Server 的全量插件实际态快照。
// tenant/edge 身份必须来自已鉴权 edgeLink，禁止信任 payload 自报身份。
type PluginStatusData struct {
	BootID            string                         `json:"boot_id"`
	Sequence          uint64                         `json:"sequence"`
	AppliedRevision   uint64                         `json:"applied_revision"`
	Installations     []PluginInstallationStatusData `json:"installations"`
	ObservedInstances []PluginObservedInstanceData   `json:"instances"`
}

// PluginDesiredInstanceData 是 Server 权威期望态中的一个实例。
// Config 值只允许非敏感标量或 secret://<name> handle；不得携带明文 secret。
type PluginDesiredInstanceData struct {
	InstanceID string            `json:"instance_id"`
	PluginID   string            `json:"plugin_id"`
	Version    string            `json:"version"`
	Enabled    bool              `json:"enabled"`
	Isolation  string            `json:"isolation"`
	Config     map[string]string `json:"config,omitempty"`
}

// PluginDesiredData 是 Server→Edge 的声明式全量期望态快照。
// SnapshotDigest 绑定同 revision 的规范化 payload，防止同 revision 不同内容被接受。
type PluginDesiredData struct {
	Revision       uint64                      `json:"revision"`
	SnapshotDigest string                      `json:"snapshot_digest"`
	Instances      []PluginDesiredInstanceData `json:"instances"`
}

// PluginApplyResultData 是单实例 reconcile 结果。Detail 必须有长度上限并经过脱敏。
type PluginApplyResultData struct {
	InstanceID string `json:"instance_id"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
}

// Plugin ack 稳定状态值。
const (
	PluginAckApplied  = "applied"
	PluginAckRejected = "rejected"
	PluginAckFailed   = "failed"
)

// PluginAckData 是 Edge→Server 的 revision 应用结果。只有 Applied 才允许 Server
// 推进 applied_revision；Rejected/Failed 保持上一完整 revision。
type PluginAckData struct {
	Revision       uint64                  `json:"revision"`
	SnapshotDigest string                  `json:"snapshot_digest"`
	Status         string                  `json:"status"`
	Results        []PluginApplyResultData `json:"results,omitempty"`
}

// ---- 鉴权与多租户（docs/api.md §2）----

// UserRole 是用户角色：admin 管理、operator 可读可写、viewer 只读。
type UserRole string

const (
	RoleAdmin    UserRole = "admin"
	RoleOperator UserRole = "operator"
	RoleViewer   UserRole = "viewer"
)

// UserView 是鉴权相关端点的用户视图（/api/auth/login、/api/auth/me、用户管理）。
type UserView struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	TenantID   int64  `json:"tenant_id"`
	TenantSlug string `json:"tenant_slug"`
	Disabled   bool   `json:"disabled,omitempty"`
}

// TenantView 是租户视图。
type TenantView struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// SessionView 是服务端会话视图（内部管理用，不直接透出会话 ID）。
type SessionView struct {
	ID         string `json:"id"`
	UserID     int64  `json:"user_id"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"`
	LastSeenAt int64  `json:"last_seen_at"`
}

// TokenView 是租户服务令牌视图（docs/api.md §3.3）。明文只在创建响应中出现一次，
// 由 handler 单独拼入 `token` 字段，本结构永不携带明文。
type TokenView struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  int64    `json:"created_at"`
	ExpiresAt  *int64   `json:"expires_at,omitempty"`
	LastUsedAt *int64   `json:"last_used_at,omitempty"`
	RevokedAt  *int64   `json:"revoked_at,omitempty"`
}

// ---- 概览与插件实例管理视图（docs/api.md §3.6 / §3.7）----

// OverviewView 是 WebUI Overview 页的一次性聚合读面。
// 所有计数都来自真实 edge 上报与 Server 权威态，禁止用占位/假数据填充。
type OverviewView struct {
	DevicesOnline  int           `json:"devices_online"`
	DevicesTotal   int           `json:"devices_total"`
	EdgesOnline    int           `json:"edges_online"`
	EdgesTotal     int           `json:"edges_total"`
	PluginsActive  int           `json:"plugins_active"`
	PluginsDesired int           `json:"plugins_desired"`
	CommandsFailed int           `json:"commands_failed"`
	RecentEvents   []EventView   `json:"recent_events"`
	OfflineDevices []DeviceView  `json:"offline_devices"`
	FailedCommands []CommandView `json:"failed_commands"`
	ServerTime     int64         `json:"server_time"`
}

// PluginInstanceDesiredView 是 Server 权威期望态的只读视图。
// SecretRefs 只含 secret://<name> handle 名称，永不携带明文。
type PluginInstanceDesiredView struct {
	InstanceID string            `json:"instance_id"`
	PluginID   string            `json:"plugin_id"`
	Version    string            `json:"version"`
	Enabled    bool              `json:"enabled"`
	Isolation  string            `json:"isolation"`
	Config     map[string]string `json:"config,omitempty"`
	SecretRefs []string          `json:"secret_refs,omitempty"`
	Revision   uint64            `json:"revision"`
	UpdatedAt  int64             `json:"updated_at"`
}

// PluginInstanceObservedView 是 Edge 上报投影的只读视图。
// 只有 Edge 真实上报过才存在；Server 不得凭空合成。
type PluginInstanceObservedView struct {
	State        string `json:"state"`
	Health       string `json:"health"`
	Version      string `json:"version,omitempty"`
	Detail       string `json:"detail,omitempty"`
	RestartCount int    `json:"restart_count"`
	LastHealthy  int64  `json:"last_healthy,omitempty"`
	ReportedAt   int64  `json:"reported_at,omitempty"`
}

// PluginInstanceView 是 Catalog/UI 的合成视图：desired 与 observed 永远分开呈现，
// 绝不把「期望启用」渲染成「实际健康」。
type PluginInstanceView struct {
	ID              string                      `json:"id"`
	TenantID        int64                       `json:"tenant_id"`
	EdgeID          string                      `json:"edge_id"`
	Desired         PluginInstanceDesiredView   `json:"desired"`
	HasObserved     bool                        `json:"has_observed"`
	Observed        *PluginInstanceObservedView `json:"observed,omitempty"`
	EdgeOnline      bool                        `json:"edge_online"`
	DesiredRevision uint64                      `json:"desired_revision"`
	AppliedRevision uint64                      `json:"applied_revision"`
	Drift           bool                        `json:"drift"`
	Stale           bool                        `json:"stale"`
	LastAckAt       int64                       `json:"last_ack_at,omitempty"`
}

// PluginInstanceListResponse 是 GET /api/plugin-instances 的载荷。
type PluginInstanceListResponse struct {
	Instances []PluginInstanceView `json:"instances"`
}

// PluginInstanceCreateRequest 是 POST /api/plugin-instances 的载荷。
// Config 值只允许非敏感标量或 secret://<name> handle；明文 secret 一律拒绝。
type PluginInstanceCreateRequest struct {
	EdgeID     string            `json:"edge_id"`
	InstanceID string            `json:"instance_id"`
	PluginID   string            `json:"plugin_id"`
	Version    string            `json:"version"`
	Enabled    *bool             `json:"enabled,omitempty"`
	Isolation  string            `json:"isolation,omitempty"`
	Config     map[string]string `json:"config,omitempty"`
	SecretRefs []string          `json:"secret_refs,omitempty"`
	// ConfirmPermissions 为 true 时表示调用者已确认插件权限扩大；
	// 缺省 false 时权限扩大请求必须被拒绝且不产生新 revision。
	ConfirmPermissions bool `json:"confirm_permissions,omitempty"`
}

// PluginInstanceUpdateRequest 是 PATCH /api/plugin-instances/{id} 的载荷。
// 所有字段可选；只更新出现的字段。
type PluginInstanceUpdateRequest struct {
	Version            *string           `json:"version,omitempty"`
	Enabled            *bool             `json:"enabled,omitempty"`
	Isolation          *string           `json:"isolation,omitempty"`
	Config             map[string]string `json:"config,omitempty"`
	SecretRefs         []string          `json:"secret_refs,omitempty"`
	ConfirmPermissions bool              `json:"confirm_permissions,omitempty"`
}

// PluginInstanceDeleteRequest 是 DELETE /api/plugin-instances/{id} 的载荷。
// 默认保留插件数据；只有显式 Purge=true（要求 admin）才删除本地数据。
type PluginInstanceDeleteRequest struct {
	Purge bool `json:"purge,omitempty"`
}

// PluginInstanceWriteResponse 是所有插件实例写操作的统一响应。
// Revision 是写成功后 tenant/edge 的新 desired revision；Accepted 表示 Edge 是否已 ack。
type PluginInstanceWriteResponse struct {
	ID        string             `json:"id"`
	Revision  uint64             `json:"revision"`
	RequestID string             `json:"request_id"`
	Instance  PluginInstanceView `json:"instance"`
}

// PluginInstanceActionRequest 是 POST /api/plugin-instances/{id}/reconcile 的载荷。
type PluginInstanceActionRequest struct {
	Force bool `json:"force,omitempty"`
}

// 插件实例写操作的稳定错误码。前端按码呈现，不解析错误文本。
const (
	PluginErrNotFound          = "plugin_instance_not_found"
	PluginErrConflict          = "plugin_instance_conflict"
	PluginErrQuota             = "plugin_quota_exceeded"
	PluginErrPermissionConfirm = "plugin_permission_confirmation_required"
	PluginErrEdgeOffline       = "plugin_edge_offline"
	PluginErrSecretForbidden   = "plugin_secret_forbidden"
	PluginErrInvalidConfig     = "plugin_invalid_config"
)
