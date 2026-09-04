package edge

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
)

// pluginApplyTimeout 是应用一份期望态快照的上限：可能要注册安装物、启动插件
// 进程并等握手，因此比设备命令宽松；超时即整份快照 failed（revision 不前进）。
const pluginApplyTimeout = 90 * time.Second

// pluginStatusMinInterval 是 plugin_status 周期上报的最小间隔，避免把
// report_interval_s 配得过小时刷爆上报通道。
const pluginStatusMinInterval = 5 * time.Second

// WithPluginSync 注入插件控制面收敛器（control-plane-sync.md §3.2/§4/§7/§8）。
// nil 等价于本 Edge 不承载插件面：plugin_desired 被忽略并记 debug，不断连。
func WithPluginSync(s *plugincontrol.Syncer) RunOption {
	return func(o *runOptions) { o.sync = s }
}

// onPluginDesired 处理 Server 下发的期望态全量快照并回 plugin_ack。
//
// revision 规则、幂等、fail-closed 与 applied cache 全在 Syncer 内；本函数只负责
// 协议编解码与上报，不做任何期望态判断（Edge 侧只有一个判断入口，避免两套规则）。
func (e *Edge) onPluginDesired(env api.Envelope) {
	if e.sync == nil {
		slog.Debug("plugin_desired ignored: plugin control plane disabled on this edge")
		return
	}
	var desired api.PluginDesiredData
	if err := json.Unmarshal(env.Data, &desired); err != nil {
		slog.Warn("bad plugin_desired payload", "err", err)
		return
	}
	base := e.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, pluginApplyTimeout)
	defer cancel()

	ack := e.sync.HandleDesired(ctx, desired)
	e.client.enqueue(api.Envelope{
		V: api.Version, Type: api.MsgPluginAck, Ts: time.Now().Unix(), Data: mustJSON(ack),
	})
	slog.Info("plugin desired handled", "revision", ack.Revision, "status", ack.Status,
		"instances", len(desired.Instances), "applied_revision", e.sync.AppliedRevision())
	// 应用后立即上报实际态：Server/UI 不必等下一拍心跳就能看到 observed 变化。
	e.reportPluginStatus()
	// 新装/启用的 Driver 会带来新的 Capability 文档；停用/卸载则要从 catalog 消失。
	// 全量覆盖语义 + 指纹抑制，因此这里无条件调用也不会刷重复消息。
	e.reportCapabilities(false)
}

// reportPluginStatus 上报一次 plugin_status 全量快照
// （boot_id + 单调 sequence + applied_revision + 安装物 + 实例实际态）。
func (e *Edge) reportPluginStatus() {
	if e.sync == nil || e.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status := e.sync.Status(ctx)
	e.client.enqueue(api.Envelope{
		V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(), Data: mustJSON(status),
	})
	slog.Debug("plugin status reported", "boot_id", status.BootID, "sequence", status.Sequence,
		"applied_revision", status.AppliedRevision,
		"installations", len(status.Installations), "instances", len(status.ObservedInstances))
}

// pluginStatusLoop 周期性重报实际态：Server 据此判断投影是否 stale，
// 也让「Edge 在线但没有任何期望态变更」时仍有可观测心跳。
func (e *Edge) pluginStatusLoop(ctx context.Context) {
	interval := time.Duration(e.cfg.ReportIntervalS) * time.Second
	if interval < pluginStatusMinInterval {
		interval = pluginStatusMinInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.reportPluginStatus()
		}
	}
}
