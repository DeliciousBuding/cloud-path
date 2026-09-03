// Package storeport 是 Server lane 对插件控制面持久化的本地端口（依赖倒置接缝）。
//
// 契约来源：.local/plan/v0.1-completion.md §3（Store lane 在 internal/store 提供同签名方法）。
// merge 时由 Captain 用一个薄适配器把 *store.Store 接到本接口：现成适配器见
// adapter_sqlite.go（默认被 build tag 排除，Store v7/v8 合并后删掉 tag 行即可编译）。
//
// 本包刻意**不 import internal/store**：STORE 与 SERVER 两条 lane 并行开发时，
// Server 侧必须能独立编译与测试。测试与开发态用 NewMemory() 的进程内实现。
//
// # 接线指引（Captain，唯一一处）
//
// Store v7/v8 合并后，在 cmd/cloudpath-server/main.go 的 server.New(server.Config{...})
// 里加一行 `PluginStore: <adapter>`，adapter 是 internal/store → 本接口的薄映射：
// 逐个方法转发，并把 store 的行类型与本包行类型互转（字段一一同名，见下），
// 同时把 store 的配额/冲突/未找到错误映射到本包的 ErrQuota / ErrConflict /
// ErrNotFound / ErrTenantMismatch（Server 据 errors.Is 产出 api.PluginErr* 稳定码）。
// 未接线时 Server 行为是安全降级：插件写 API 503、WS plugin_* 按旧协议忽略、读面真实为空。
//
// 实现方必须满足的语义不变量：
//   - 所有读写按 tenantID 作用域；任何 upsert 不得修改既有行的 tenant_id；
//   - Create/Update/Delete 在同一写事务内更新期望态并把 tenant/edge desired revision +1，
//     返回新 revision；失败不得留下半状态；
//   - 配额超限必须整体失败（不写入、不增 revision），返回 ErrQuota；
//   - 删除期望态不删除审计；purge=false 时必须保留 observed 投影行；
//   - 只存 secret://<name> handle 名与非敏感标量，绝不存明文 secret。
package storeport

import (
	"errors"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// 端口级稳定哨兵错误。适配器必须把底层实现的等价失败映射到这些值，
// Server 侧据 errors.Is 映射为 api.PluginErr* 稳定错误码（前端按码呈现）。
var (
	// ErrNotFound 表示目标期望态行不存在（Update/Delete 命中空）。
	ErrNotFound = errors.New("storeport: plugin instance not found")
	// ErrConflict 表示 (tenant, edge, instance) 已存在或身份冲突。
	ErrConflict = errors.New("storeport: plugin instance conflict")
	// ErrQuota 表示租户插件实例配额已满：不得写入、不得增加 revision。
	ErrQuota = errors.New("storeport: plugin instance quota exceeded")
	// ErrTenantMismatch 表示既有行属于其他租户（fail-closed，绝不改写归属）。
	ErrTenantMismatch = errors.New("storeport: row owned by another tenant")
)

// PluginInstanceRow 是 Server 权威期望态行（§3 逐字对齐）。
// ConfigJSON 是 map[string]string 的 JSON，值只含非敏感标量或 secret://<name> handle；
// SecretRefs 是 JSON []string，只含 handle 名。
type PluginInstanceRow struct {
	TenantID   int64
	EdgeID     string
	InstanceID string
	PluginID   string
	Version    string
	Enabled    bool
	Isolation  string
	ConfigJSON string
	SecretRefs string
	Revision   uint64
	CreatedAt  int64
	UpdatedAt  int64
}

// PluginEdgeRevisionRow 是每个 tenant/edge 的 revision 与 applied 投影（§3 逐字对齐）。
type PluginEdgeRevisionRow struct {
	TenantID        int64
	EdgeID          string
	DesiredRevision uint64
	AppliedRevision uint64
	BootID          string
	LastSequence    uint64
	LastReportAt    int64
	LastAckAt       int64
}

// PluginObservationRow 是 Edge 上报的实例实际态投影行。
// §3 只给了方法名未展开字段：本包按 api.PluginObservedInstanceData + 作用域/时间定义，
// 适配器负责与 store 行互映射。
type PluginObservationRow struct {
	TenantID     int64
	EdgeID       string
	InstanceID   string
	PluginID     string
	Version      string
	HostOnline   bool
	State        string
	Health       string
	Detail       string
	RestartCount int
	LastHealthy  int64
	MessageRate  float64
	ReportedAt   int64
}

// PluginInstallationRow 是 Edge 上报的安装物投影行（只含公开 manifest 事实）。
// Permissions/Contributions/Capabilities 以 JSON 文本承载，对应 api 的公开 DTO。
type PluginInstallationRow struct {
	TenantID          int64
	EdgeID            string
	PluginID          string
	Version           string
	Kind              string
	Protocol          int
	Digest            string
	TrustMode         string
	Verified          bool
	VerifiedPublisher string
	PermissionsJSON   string
	ContributionsJSON string
	CapabilitiesJSON  string
	ReportedAt        int64
}

// TenantPolicyRow 是租户保留期/配额行。§3 未展开字段：本包按
// docs/architecture/tenant-security-policy.md §3/§4 的资源清单定义。
// 任何 <=0 的字段表示 NULL/继承 Server 默认值（绝不用 0 表示无限）。
type TenantPolicyRow struct {
	TenantID              int64
	RetentionEvents       int
	RetentionCommands     int
	RetentionAudit        int
	RetentionTokens       int
	RetentionObservations int
	QuotaDevices          int
	QuotaEdges            int
	QuotaBrowserWS        int
	QuotaTokens           int
	QuotaUsers            int
	QuotaEventsPerMinute  int
	QuotaPluginInstances  int
	UpdatedAt             int64
}

// PluginStore 是 Server lane 消费的插件控制面持久化端口（§3 全部方法，签名逐字对齐）。
// *store.Store 经适配器满足本接口；nil 表示 API-only 形态，调用方必须判空降级。
type PluginStore interface {
	// ---- 插件期望态（Server 权威）----
	ListPluginInstancesTenant(tenantID int64) ([]PluginInstanceRow, error)
	GetPluginInstance(tenantID int64, edgeID, instanceID string) (PluginInstanceRow, bool, error)
	CreatePluginInstance(row PluginInstanceRow) (uint64, error)
	UpdatePluginInstance(row PluginInstanceRow) (uint64, error)
	DeletePluginInstance(tenantID int64, edgeID, instanceID string, purge bool) (uint64, error)
	PluginDesiredRevision(tenantID int64, edgeID string) (uint64, error)

	// ---- Edge revision / applied 投影 ----
	GetPluginEdgeRevision(tenantID int64, edgeID string) (PluginEdgeRevisionRow, error)
	SetPluginEdgeApplied(tenantID int64, edgeID, bootID string, seq, appliedRevision uint64, at int64) error
	SetPluginEdgeReport(tenantID int64, edgeID, bootID string, seq uint64, at int64) error

	// ---- Edge 上报的只读投影 ----
	UpsertPluginInstallations(tenantID int64, edgeID string, rows []api.PluginInstallationStatusData) error
	UpsertPluginObservations(tenantID int64, edgeID string, rows []api.PluginObservedInstanceData, reportedAt int64) error
	ListPluginObservationsTenant(tenantID int64) ([]PluginObservationRow, error)
	ListPluginInstallationsTenant(tenantID int64) ([]PluginInstallationRow, error)

	// ---- 租户策略 / 配额 / 保留期 ----
	GetTenantPolicy(tenantID int64) (TenantPolicyRow, error)
	SetTenantPolicy(tenantID int64, p TenantPolicyRow) error
	CountPluginInstances(tenantID int64) (int, error)
}
