package plugincontrol

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

// HostObserver 由支持实际态读取的 Manager 可选实现（*pluginhost.Manager 满足）。
// 刻意与 HostManager 分开：观测能力缺席时 Observe 诚实回报「不可观测」，
// 而不是编造健康态；既有测试替身也不必被迫实现观测方法。
type HostObserver interface {
	ListInstances(tenant string) []pluginhost.InstanceSnapshot
}

// MetricsReader 由 Manager 可选实现：提供 message rate 与 last healthy。
type MetricsReader interface {
	Metrics(tenant, id string) (pluginhost.Metrics, error)
}

// Host 是 Applier 的生产实现。
var _ Applier = (*Host)(nil)

// ApplySnapshot 把一份**完整**期望态快照收敛到本地，返回逐实例结果。
//
// 声明式语义（control-plane-sync.md §1/§8）：只收敛快照描述的最终态，
// 不回放断线期间的中间操作；因此本方法幂等，可用同一份快照重复调用。
//
// 快照里没有、但本地还留着的实例 = Server 已删除：停用并移除，
// 默认保留插件数据（purge 是 Server 侧显式高风险选项，Edge 不自作主张）。
func (h *Host) ApplySnapshot(ctx context.Context, tenant string, instances []api.PluginDesiredInstanceData) ([]api.PluginApplyResultData, error) {
	tenant = NormalizeTenant(tenant)
	if _, err := h.registerInstallations(ctx); err != nil {
		// 安装物注册失败影响全部实例：逐实例 failed（而不是整体 error），
		// 让 Server 能看清到底哪些实例没应用上。
		h.opts.Logger.Warn("plugin installations unavailable; snapshot not applied",
			"tenant", tenant, "err", SanitizeDetail(err.Error()))
		return allFailed(instances, err.Error()), nil
	}
	present := make(map[string]bool, len(instances))
	results := make([]api.PluginApplyResultData, 0, len(instances))
	for _, inst := range instances {
		if id := strings.TrimSpace(inst.InstanceID); id != "" {
			present[id] = true
		}
		results = append(results, h.applyOne(ctx, tenant, inst))
	}
	h.retireAbsent(tenant, present)
	return results, nil
}

// applyOne 应用单个期望实例：secret 双校验 → 收敛 Manager → 成功后才持久化。
func (h *Host) applyOne(ctx context.Context, tenant string, inst api.PluginDesiredInstanceData) api.PluginApplyResultData {
	id := strings.TrimSpace(inst.InstanceID)
	ok := func(detail string) api.PluginApplyResultData {
		return api.PluginApplyResultData{InstanceID: id, Status: api.PluginAckApplied, Detail: detail}
	}
	bad := func(detail string) api.PluginApplyResultData {
		return api.PluginApplyResultData{InstanceID: id, Status: api.PluginAckFailed, Detail: SanitizeDetail(detail)}
	}

	pluginID := strings.TrimSpace(inst.PluginID)
	version := strings.TrimSpace(inst.Version)
	switch {
	case id == "":
		return bad("invalid_instance: instance_id 必填")
	case pluginID == "" || version == "":
		return bad("invalid_instance: plugin_id 与 version 必填")
	case !validStateSegment(id):
		return bad(fmt.Sprintf("invalid_instance: instance id %q 不是合法标识", id))
	}
	isolation, err := ParseIsolation(inst.Isolation)
	if err != nil {
		return bad(err.Error())
	}

	// §7 双校验 + fail-closed：manifest 已声明该 secret 名 **且** 实例配置显式绑定
	// 该 handle 才解析；handle 不存在/已吊销/路径不安全/声明不符一律整实例失败，
	// 绝不回落旧明文缓存。明文只活在这几行里，随后立即清零丢弃。
	if handles := BoundHandles(inst.Config); len(handles) > 0 {
		if h.opts.Secrets == nil {
			return bad(fmt.Sprintf("%v: 实例绑定 %s，但本 Edge 未配置本地 secret provider",
				ErrSecretForbidden, strings.Join(handles, ",")))
		}
		plain, err := h.opts.Secrets.Resolve(tenant, id, pluginID, inst.Config)
		if err != nil {
			return bad(err.Error())
		}
		DiscardSecrets(plain)
	}

	// 持久化顺序契约：先收敛 Manager，成功后才写状态文件。状态文件是
	// Edge 离线重启时的唯一 replay 依据（control-plane-sync.md §8「从本地
	// applied cache 启动」），必须始终停留在最后一个可满足的配置上。
	// 若先写 desired 再收敛，一次 installation-not-found 的失败 apply 会把
	// 不可满足的版本留在盘上：applied_revision 不前进，但实例文件已超前，
	// 下次进程重启 replay 即整体硬失败（2026-09-05 生产事故：desired 引用
	// driver@0.2.1 而本地只装了 0.1.0，edge 从此无法自举）。
	// Config 只含非敏感标量与 secret:// handle，明文永不落盘。
	state := InstanceState{
		Tenant: tenant, InstanceID: id, PluginID: pluginID, Version: version,
		Enabled: inst.Enabled, Isolation: FormatIsolation(isolation),
		Config: cloneConfig(inst.Config),
	}

	if !inst.Enabled {
		if err := h.opts.Manager.Disable(tenant, id); err != nil && !errors.Is(err, pluginhost.ErrInstanceNotFound) {
			return bad(err.Error())
		}
		if err := h.opts.Store.Save(state); err != nil {
			return bad(err.Error())
		}
		return ok("disabled")
	}
	spec := pluginhost.InstanceSpec{
		ID: id, Tenant: tenant, PluginID: pluginID, Version: version,
		Config: configForState(state), Isolation: isolation,
	}
	if _, err := h.opts.Manager.CreateInstance(spec); err != nil && !errors.Is(err, pluginhost.ErrInstanceExists) {
		return bad(err.Error())
	}
	if err := h.opts.Manager.Start(tenant, id); err != nil {
		return bad(err.Error())
	}
	if err := h.opts.Store.Save(state); err != nil {
		return bad(err.Error())
	}
	return ok("enabled")
}

// retireAbsent 停用并移除快照里已不存在的本地实例（Server 侧已删除）。
func (h *Host) retireAbsent(tenant string, present map[string]bool) {
	states, err := h.opts.Store.ListTenant(tenant)
	if err != nil {
		h.opts.Logger.Warn("list local plugin instances failed", "tenant", tenant, "err", SanitizeDetail(err.Error()))
		return
	}
	for _, state := range states {
		if present[state.InstanceID] {
			continue
		}
		if err := h.opts.Manager.Disable(tenant, state.InstanceID); err != nil && !errors.Is(err, pluginhost.ErrInstanceNotFound) {
			h.opts.Logger.Warn("disable retired plugin instance failed",
				"instance", state.InstanceID, "err", SanitizeDetail(err.Error()))
		}
		// 默认保留插件数据（不 purge）。
		if _, err := h.opts.Manager.Remove(tenant, state.InstanceID); err != nil && !errors.Is(err, pluginhost.ErrInstanceNotFound) {
			h.opts.Logger.Warn("remove retired plugin instance failed",
				"instance", state.InstanceID, "err", SanitizeDetail(err.Error()))
		}
		if err := h.opts.Store.Delete(tenant, state.InstanceID); err != nil {
			h.opts.Logger.Warn("delete retired plugin instance state failed",
				"instance", state.InstanceID, "err", SanitizeDetail(err.Error()))
			continue
		}
		h.opts.Logger.Info("plugin instance retired by server snapshot",
			"tenant", tenant, "instance", state.InstanceID, "data", "preserved")
	}
}

// Observe 返回本地安装物与实例实际态（Edge 是实际态的唯一观测源）。
func (h *Host) Observe(ctx context.Context, tenant string) ([]api.PluginInstallationStatusData, []api.PluginObservedInstanceData, error) {
	tenant = NormalizeTenant(tenant)
	installations, err := h.InstallationStatus()
	return installations, h.InstanceObservations(tenant), err
}

// InstallationStatus 从 plugins.lock + 已安装 manifest 组装安装物公开事实。
//
// 上报红线：**不含本地路径**（Installation.Path 刻意不出现在这里）、
// 不含启动参数与环境变量；permissions.secrets 只携带 secret 名称，永不是值。
func (h *Host) InstallationStatus() ([]api.PluginInstallationStatusData, error) {
	lock, err := registry.LoadLockFile(h.opts.LockPath)
	if err != nil {
		return nil, err
	}
	out := make([]api.PluginInstallationStatusData, 0, len(lock.Plugins))
	for _, locked := range lock.Plugins {
		row := api.PluginInstallationStatusData{
			PluginID: locked.ID, Version: locked.Version, Digest: locked.Digest,
			TrustMode: string(locked.Mode), Verified: locked.Verified,
			VerifiedPublisher: locked.VerifiedPublisher, Protocol: locked.Protocol,
		}
		path := filepath.Join(h.opts.PluginsDir, registry.SafePluginID(locked.ID), "plugin.yaml")
		manifest, err := registry.ReadManifest(path)
		if err != nil {
			// manifest 不可读：诚实标注 unknown，绝不编造公开字段。
			row.Kind = "unknown"
			out = append(out, row)
			continue
		}
		row.Kind = manifest.Kind
		if row.Protocol == 0 {
			row.Protocol = manifest.Protocol
		}
		row.Permissions = api.PluginPermissionsData{
			Hardware:   append([]string(nil), manifest.Permissions.Hardware...),
			Network:    append([]string(nil), manifest.Permissions.Network...),
			Filesystem: append([]string(nil), manifest.Permissions.Filesystem...),
			Secrets:    append([]string(nil), manifest.Permissions.Secrets...),
		}
		row.Capabilities = append([]string(nil), manifest.Capabilities...)
		if c := manifest.Contributes; c != nil {
			for _, d := range c.Drivers {
				row.Contributions.Drivers = append(row.Contributions.Drivers, api.PluginDriverContributionData{
					ID: d.ID, Title: d.Title, Discovery: d.Discovery,
				})
			}
			for _, a := range c.Applications {
				row.Contributions.Applications = append(row.Contributions.Applications, api.PluginApplicationContributionData{
					ID: a.ID, Title: a.Title,
				})
			}
			for _, c2 := range c.Connectors {
				row.Contributions.Connectors = append(row.Contributions.Connectors, api.PluginConnectorContributionData{
					ID: c2.ID, Title: c2.Title, Direction: c2.Direction, Host: c2.Host,
				})
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PluginID != out[j].PluginID {
			return out[i].PluginID < out[j].PluginID
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// InstanceObservations 返回本租户实例的实际态。
//
// desired 与 observed 永远分开：Manager 不可观测时回报 HostOnline=false +
// STOPPED/UNKNOWN，绝不把「期望启用」渲染成「实际健康」。
func (h *Host) InstanceObservations(tenant string) []api.PluginObservedInstanceData {
	tenant = NormalizeTenant(tenant)
	if observer, ok := h.opts.Manager.(HostObserver); ok {
		snaps := observer.ListInstances(tenant)
		metrics, hasMetrics := h.opts.Manager.(MetricsReader)
		out := make([]api.PluginObservedInstanceData, 0, len(snaps))
		for _, snap := range snaps {
			row := api.PluginObservedInstanceData{
				InstanceID: snap.InstanceID, PluginID: snap.PluginID, Version: snap.Version,
				HostOnline: true, State: snap.State.String(), Health: snap.Health.String(),
				RestartCount: snap.Restarts, Detail: instanceDetail(snap),
			}
			if hasMetrics {
				if m, err := metrics.Metrics(tenant, snap.InstanceID); err == nil {
					row.MessageRate = m.MessageRate
					if !m.LastHealthy.IsZero() {
						row.LastHealthy = m.LastHealthy.Unix()
					}
				}
			}
			out = append(out, row)
		}
		sortObservations(out)
		return out
	}
	states, err := h.opts.Store.ListTenant(tenant)
	if err != nil {
		h.opts.Logger.Warn("list plugin instance states failed", "tenant", tenant, "err", SanitizeDetail(err.Error()))
		return nil
	}
	out := make([]api.PluginObservedInstanceData, 0, len(states))
	for _, state := range states {
		out = append(out, api.PluginObservedInstanceData{
			InstanceID: state.InstanceID, PluginID: state.PluginID, Version: state.Version,
			HostOnline: false, State: pluginhost.StateStopped.String(),
			Health: pluginhost.HealthUnknown.String(), Detail: "host_not_observable",
		})
	}
	sortObservations(out)
	return out
}

// instanceDetail 只由计数事实组成：绝不含插件 stdout/stderr 原文、
// 命令行、环境变量或本机路径（不变量 6）。
func instanceDetail(snap pluginhost.InstanceSnapshot) string {
	var parts []string
	if !snap.Enabled {
		parts = append(parts, "disabled")
	}
	if snap.Restarts > 0 {
		parts = append(parts, fmt.Sprintf("restarts=%d", snap.Restarts))
	}
	if snap.Crashes > 0 {
		parts = append(parts, fmt.Sprintf("crashes=%d", snap.Crashes))
	}
	if snap.ConsecutiveFailures > 0 {
		parts = append(parts, fmt.Sprintf("health_failures=%d", snap.ConsecutiveFailures))
	}
	if snap.Launches > 0 {
		parts = append(parts, fmt.Sprintf("launches=%d", snap.Launches))
	}
	return strings.Join(parts, " ")
}

func sortObservations(out []api.PluginObservedInstanceData) {
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
}

// NormalizeTenant 归一租户标识：空值一律落到 default，绝不落到「全部租户」。
func NormalizeTenant(tenant string) string {
	if t := strings.TrimSpace(tenant); t != "" {
		return t
	}
	return "default"
}

func cloneConfig(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
