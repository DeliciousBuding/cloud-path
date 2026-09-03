package plugincontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// Applier 把 Server 下发的**完整**期望态快照应用到本地，并回报本地实际态。
//
// 生产实现是 *Host（驱动 pluginhost.Manager + 本地 desired Store）；测试注入
// fake，任何测试都不启动真实第三方插件进程。
//
// 声明式语义：Applier 只收敛「快照描述的最终态」，不回放断线期间的中间操作
// （control-plane-sync.md §1/§8）。因此 ApplySnapshot 必须幂等。
type Applier interface {
	// ApplySnapshot 应用一份完整期望态快照，返回**逐实例**结果（每实例一条）。
	// 只要有一条不是 applied，调用方就不推进 revision。
	ApplySnapshot(ctx context.Context, tenant string, instances []api.PluginDesiredInstanceData) ([]api.PluginApplyResultData, error)
	// Observe 返回本地安装物与实例实际态。实现必须自行脱敏：
	// 不含明文 secret、访问令牌、本机绝对路径、插件 stdout/stderr 原文。
	Observe(ctx context.Context, tenant string) ([]api.PluginInstallationStatusData, []api.PluginObservedInstanceData, error)
}

// AppliedCache 是 Edge 本地插件控制面缓存（control-plane-sync.md §3.2）：
// 最近一次成功应用的 revision、Server 下发的规范化摘要、每实例结果。
//
// 刻意**不保存 boot_id**：boot_id 必须每次进程启动都换新（§3.3），从缓存恢复
// 旧 boot_id 会让 Server 无法识别进程重启、也无法拒绝旧连接的迟到消息。
type AppliedCache struct {
	AppliedRevision uint64                      `json:"applied_revision"`
	SnapshotDigest  string                      `json:"snapshot_digest"`
	Results         []api.PluginApplyResultData `json:"results,omitempty"`
	UpdatedAt       int64                       `json:"updated_at,omitempty"`
}

// IsEmpty 报告是否从未成功应用过任何 revision。
func (c AppliedCache) IsEmpty() bool {
	return c.AppliedRevision == 0 && strings.TrimSpace(c.SnapshotDigest) == ""
}

// ErrCacheCorrupt 表示本地 applied cache 存在但不可解析。
var ErrCacheCorrupt = errors.New("plugincontrol: applied cache corrupt")

// LoadAppliedCache 读取本地 applied cache。文件缺失 = 从未应用过（零值，非错误）。
// 文件损坏返回 ErrCacheCorrupt：调用方必须显式记录，然后以零值继续
// （声明式全量快照本身就能把 Edge 收敛到 Server 的当前真相，不会因此跑偏）。
func LoadAppliedCache(path string) (AppliedCache, error) {
	if strings.TrimSpace(path) == "" {
		return AppliedCache{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return AppliedCache{}, nil
	}
	if err != nil {
		return AppliedCache{}, fmt.Errorf("%w: %v", ErrCacheCorrupt, reasonOf(err))
	}
	var cache AppliedCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return AppliedCache{}, fmt.Errorf("%w: %v", ErrCacheCorrupt, err)
	}
	for i := range cache.Results {
		cache.Results[i].Detail = SanitizeDetail(cache.Results[i].Detail)
	}
	return cache, nil
}

// SaveAppliedCache 用同目录临时文件 + fsync + 原子 rename 落盘（§3.2 要求）。
// 空路径表示不持久化（进程重启后从 revision 0 开始，靠 Server 全量快照收敛）。
func SaveAppliedCache(path string, cache AppliedCache) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal applied cache: %w", err)
	}
	data = append(data, '\n')
	if err := writeFileAtomic(filepath.Clean(path), data); err != nil {
		return err
	}
	return os.Chmod(filepath.Clean(path), 0o600)
}

// SyncOptions 配置 Edge 侧插件控制面收敛器。
type SyncOptions struct {
	// Tenant 是本 Edge 归属的租户（缺省 default）。期望态/实际态都按它隔离。
	Tenant string
	// CachePath 是本地 applied cache 路径；空 = 不持久化。
	CachePath string
	// Applier 必填。
	Applier Applier
	Logger  *slog.Logger
	Now     func() time.Time
}

// Syncer 是 Edge 侧插件控制面收敛器：revision 规则、本地 applied cache、
// plugin_status 上报组装（control-plane-sync.md §3.2/§4/§8）。
//
// 并发：HandleDesired 全程持锁，因此即使 Server 连发多份快照、或读循环为每条
// 消息各起协程，应用也严格串行，绝不会出现两份快照交错写本地状态。
type Syncer struct {
	opts SyncOptions

	mu       sync.Mutex
	bootID   string
	sequence uint64
	cache    AppliedCache
}

// NewSyncer 校验选项、生成本次进程的 boot_id 并载入本地 applied cache。
func NewSyncer(opts SyncOptions) (*Syncer, error) {
	if opts.Applier == nil {
		return nil, fmt.Errorf("%w: applier is required", ErrInvalidState)
	}
	if strings.TrimSpace(opts.Tenant) == "" {
		opts.Tenant = "default"
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	bootID, err := newBootID()
	if err != nil {
		return nil, err
	}
	cache, err := LoadAppliedCache(opts.CachePath)
	if err != nil {
		// 不静默：损坏的 cache 必须留下痕迹，然后以零值继续（全量快照可收敛）。
		opts.Logger.Warn("plugin applied cache unusable; starting from revision 0",
			"err", err, "status", "DEGRADED")
		cache = AppliedCache{}
	}
	return &Syncer{opts: opts, bootID: bootID, cache: cache}, nil
}

// newBootID 生成不透明的进程启动标识（进程重启即换新）。
func newBootID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate boot id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// BootID 返回本次进程的启动标识。
func (s *Syncer) BootID() string { return s.bootID }

// AppliedRevision 返回最近一次成功应用的 revision。
func (s *Syncer) AppliedRevision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cache.AppliedRevision
}

// Cache 返回本地 applied cache 的副本（诊断/测试用）。
func (s *Syncer) Cache() AppliedCache {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.cache
	out.Results = append([]api.PluginApplyResultData(nil), s.cache.Results...)
	return out
}

// HandleDesired 按契约规则处理一份 Server 期望态快照并产出 ack：
//
//   - new revision > applied            → apply，成功才推进 applied_revision
//   - same revision + same digest       → 幂等 ACK，不重复 apply（无副作用）
//   - same revision + different digest  → reject（协议错误，fail-closed，applied 不变）
//   - old revision                      → reject（迟到/倒序消息不得让本地状态倒退）
//   - 空 digest                         → reject（无法验证同 revision 内容一致性）
//   - 部分实例失败                       → failed，revision **不前进**，逐实例报告
func (s *Syncer) HandleDesired(ctx context.Context, desired api.PluginDesiredData) api.PluginAckData {
	s.mu.Lock()
	defer s.mu.Unlock()

	digest := strings.TrimSpace(desired.SnapshotDigest)
	if digest == "" {
		return s.rejectLocked(desired, digest, "protocol_error: snapshot_digest 缺失，无法验证同 revision 内容一致性")
	}
	applied := s.cache.AppliedRevision
	switch {
	case desired.Revision < applied:
		return s.rejectLocked(desired, digest, fmt.Sprintf(
			"stale_revision: revision %d 小于已应用 %d（迟到消息不得让本地状态倒退）", desired.Revision, applied))
	case desired.Revision == applied && !s.cache.IsEmpty():
		if digest == s.cache.SnapshotDigest {
			// 幂等重放：同 revision 同摘要，无副作用，不重复 apply。
			return api.PluginAckData{
				Revision: desired.Revision, SnapshotDigest: digest,
				Status: api.PluginAckApplied, Results: cloneResults(s.cache.Results),
			}
		}
		return s.rejectLocked(desired, digest, fmt.Sprintf(
			"digest_mismatch: revision %d 的内容与已应用快照不一致（同 revision 不同 payload）", desired.Revision))
	}

	results, err := s.opts.Applier.ApplySnapshot(ctx, s.opts.Tenant, desired.Instances)
	if err != nil {
		s.opts.Logger.Warn("plugin snapshot apply failed", "revision", desired.Revision,
			"tenant", s.opts.Tenant, "err", SanitizeDetail(err.Error()))
		return api.PluginAckData{
			Revision: desired.Revision, SnapshotDigest: digest, Status: api.PluginAckFailed,
			Results: allFailed(desired.Instances, SanitizeDetail(err.Error())),
		}
	}
	results = normalizeResults(desired.Instances, results)
	if firstFailure(results) != "" {
		// 部分失败：保留上一个完整已应用快照，revision 不前进（§3.2/§8）。
		s.opts.Logger.Warn("plugin snapshot partially applied; revision not advanced",
			"revision", desired.Revision, "applied_revision", applied, "tenant", s.opts.Tenant)
		return api.PluginAckData{
			Revision: desired.Revision, SnapshotDigest: digest,
			Status: api.PluginAckFailed, Results: results,
		}
	}

	s.cache = AppliedCache{
		AppliedRevision: desired.Revision, SnapshotDigest: digest,
		Results: cloneResults(results), UpdatedAt: s.opts.Now().Unix(),
	}
	if err := SaveAppliedCache(s.opts.CachePath, s.cache); err != nil {
		// 应用确实成功了，不能因此谎报失败；但离线续跑能力受损，必须留痕。
		s.opts.Logger.Error("plugin applied cache persist failed; offline resume degraded",
			"revision", desired.Revision, "err", SanitizeDetail(err.Error()), "status", "DEGRADED")
	}
	return api.PluginAckData{
		Revision: desired.Revision, SnapshotDigest: digest,
		Status: api.PluginAckApplied, Results: results,
	}
}

// rejectLocked 产出 rejected ack；本地 applied cache 一律保持不变。
func (s *Syncer) rejectLocked(desired api.PluginDesiredData, digest, detail string) api.PluginAckData {
	s.opts.Logger.Warn("plugin desired snapshot rejected",
		"revision", desired.Revision, "applied_revision", s.cache.AppliedRevision,
		"tenant", s.opts.Tenant, "detail", SanitizeDetail(detail))
	return api.PluginAckData{
		Revision: desired.Revision, SnapshotDigest: digest, Status: api.PluginAckRejected,
		Results: []api.PluginApplyResultData{{
			InstanceID: "", Status: api.PluginAckRejected, Detail: SanitizeDetail(detail),
		}},
	}
}

// Status 组装一次 plugin_status 全量上报：boot_id（进程级）+ 单调 sequence +
// applied_revision + 本地安装物 + 实例实际态。
//
// 上报脱敏红线（不变量 6）：所有 detail 再过一次 SanitizeDetail 兜底，
// 即使 Applier 失手也不会把绝对路径/凭据形态/stdout 原文送出网。
func (s *Syncer) Status(ctx context.Context) api.PluginStatusData {
	s.mu.Lock()
	s.sequence++
	out := api.PluginStatusData{
		BootID:          s.bootID,
		Sequence:        s.sequence,
		AppliedRevision: s.cache.AppliedRevision,
	}
	s.mu.Unlock()

	installations, instances, err := s.opts.Applier.Observe(ctx, s.opts.Tenant)
	if err != nil {
		s.opts.Logger.Warn("plugin observe failed; reporting installations/instances as unavailable",
			"tenant", s.opts.Tenant, "err", SanitizeDetail(err.Error()))
	}
	for i := range instances {
		instances[i].Detail = SanitizeDetail(instances[i].Detail)
	}
	out.Installations = installations
	out.ObservedInstances = instances
	return out
}

// normalizeResults 保证「每个期望实例恰好一条结果」且 detail 已脱敏：
// Applier 少报的实例按 failed 补齐（缺结果本身就是失败，不能当成成功）。
func normalizeResults(instances []api.PluginDesiredInstanceData, results []api.PluginApplyResultData) []api.PluginApplyResultData {
	byID := make(map[string]api.PluginApplyResultData, len(results))
	for _, r := range results {
		r.Detail = SanitizeDetail(r.Detail)
		if r.Status == "" {
			r.Status = api.PluginAckFailed
		}
		byID[r.InstanceID] = r
	}
	out := make([]api.PluginApplyResultData, 0, len(instances))
	for _, inst := range instances {
		if r, ok := byID[inst.InstanceID]; ok {
			out = append(out, r)
			continue
		}
		out = append(out, api.PluginApplyResultData{
			InstanceID: inst.InstanceID, Status: api.PluginAckFailed,
			Detail: SanitizeDetail("applier 未回报该实例结果"),
		})
	}
	return out
}

func allFailed(instances []api.PluginDesiredInstanceData, detail string) []api.PluginApplyResultData {
	out := make([]api.PluginApplyResultData, 0, len(instances))
	for _, inst := range instances {
		out = append(out, api.PluginApplyResultData{
			InstanceID: inst.InstanceID, Status: api.PluginAckFailed, Detail: SanitizeDetail(detail),
		})
	}
	return out
}

// firstFailure 返回第一条非 applied 结果的 detail（全 applied 时返回空串）。
func firstFailure(results []api.PluginApplyResultData) string {
	for _, r := range results {
		if r.Status != api.PluginAckApplied {
			if r.Detail == "" {
				return r.InstanceID + ": " + r.Status
			}
			return r.Detail
		}
	}
	return ""
}

func cloneResults(in []api.PluginApplyResultData) []api.PluginApplyResultData {
	if len(in) == 0 {
		return nil
	}
	return append([]api.PluginApplyResultData(nil), in...)
}
