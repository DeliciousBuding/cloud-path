// Package storeport defines the persistence port used by the server-side plugin
// control plane.
//
// The package intentionally does not import internal/store. Production wiring
// adapts a concrete store implementation to PluginStore, while NewMemory provides
// an in-process implementation for tests and development.
//
// Implementations must preserve these semantics:
//   - all reads and writes are tenant-scoped; an upsert must never change an
//     existing row's tenant_id;
//   - Create/Update/Delete update desired state and increment the tenant/edge
//     desired revision in the same transaction, returning the new revision;
//     failures must not leave partial state;
//   - quota exhaustion fails the whole operation without writing or incrementing
//     the revision and returns ErrQuota;
//   - deleting desired state does not delete audit data; purge=false preserves
//     the observed projection and instance-private data, while purge=true removes
//     that instance's observed projection, domain records and scheduled tasks in
//     the same transaction;
//   - only secret://<name> handles and non-sensitive scalars are stored; plaintext
//     secrets are never persisted.
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

// PluginInstanceRow is the authoritative desired-state row for a plugin instance.
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

// PluginEdgeRevisionRow is the revision and applied projection for a tenant/edge.
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

// PluginObservationRow is the observed-state projection reported by an edge.
// Its fields mirror api.PluginObservedInstanceData plus scope and timestamp
// coordinates; adapters map between this type and concrete store rows.
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

// TenantPolicyRow 是租户保留期/配额行。字段按租户安全策略的资源清单定义；
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

// PluginStore is the persistence port consumed by the server-side plugin control plane.
// A nil value means the API-only deployment; callers must handle it by degrading safely.
type PluginStore interface {
	// ---- 插件期望态（Server 权威）----
	ListPluginInstancesTenant(tenantID int64) ([]PluginInstanceRow, error)
	GetPluginInstance(tenantID int64, edgeID, instanceID string) (PluginInstanceRow, bool, error)
	CreatePluginInstance(row PluginInstanceRow) (uint64, error)
	UpdatePluginInstance(row PluginInstanceRow) (uint64, error)
	// DeletePluginInstance 删除期望态并 +1 revision（Edge 需要收敛「实例已移除」）。
	// purge=false 必须保留该实例的 observed 投影、领域记录、定时任务与全部审计；
	// purge=true 才在同一事务中清除该实例的 observed 投影、领域记录和定时任务。
	DeletePluginInstance(tenantID int64, edgeID, instanceID string, purge bool) (uint64, error)
	PluginDesiredRevision(tenantID int64, edgeID string) (uint64, error)

	// ---- Edge revision / applied 投影 ----
	GetPluginEdgeRevision(tenantID int64, edgeID string) (PluginEdgeRevisionRow, error)
	// SetPluginEdgeApplied 写入 applied_revision 与 ack 时间。rejected/failed 的 ack
	// 也会调用它，但传入的是**未变化的** appliedRevision（只刷新 LastAckAt）。
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
