// Package api 定义 edge↔server↔浏览器 的共享契约：WS 消息信封与 REST DTO。
// 两侧二进制都只认这里的类型 —— 契约变更必须同时过前端 lib/types.ts。
package api

import "encoding/json"

// Version 是消息信封协议版本，不兼容变更时递增。
const Version = 1

// MsgType 枚举 WS 消息类型。
type MsgType string

const (
	MsgHello      MsgType = "hello"       // edge→server：鉴权+注册
	MsgSnapshot   MsgType = "snapshot"    // server→浏览器：连接时的全量快照
	MsgState      MsgType = "state"       // edge→server→浏览器：设备状态
	MsgEvent      MsgType = "event"       // edge→server→浏览器：设备事件
	MsgCommand    MsgType = "command"     // server→edge：命令下发
	MsgCommandAck MsgType = "command_ack" // edge→server→浏览器：命令回执
	MsgEdgeUp     MsgType = "edge_up"     // server→浏览器：edge 上线
	MsgEdgeDown   MsgType = "edge_down"   // server→浏览器：edge 离线
	MsgPing       MsgType = "ping"
	MsgPong       MsgType = "pong"
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
type SnapshotData struct {
	Devices []DeviceView `json:"devices"`
	Edges   []EdgeView   `json:"edges"`
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

// ---- 鉴权与多租户（docs/api.md §2）----

// UserRole 是用户角色：admin 管理、operator 可读可写、viewer 只读。
type UserRole string

const (
	RoleAdmin    UserRole = "admin"
	RoleOperator UserRole = "operator"
	RoleViewer   UserRole = "viewer"
)

// UserView 是鉴权相关端点的用户视图（/api/auth/login、/api/auth/me）。
type UserView struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	TenantID   int64  `json:"tenant_id"`
	TenantSlug string `json:"tenant_slug"`
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
