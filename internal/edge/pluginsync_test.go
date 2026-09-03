package edge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
)

// ---- 插件控制面 WS 接线（control-plane-sync.md §4）----

// fakeApplier 是 plugincontrol.Applier 的替身：任何测试都不启动真实插件进程。
type fakeApplier struct {
	mu            sync.Mutex
	snapshots     [][]string
	results       []api.PluginApplyResultData
	resultsErr    error
	installations []api.PluginInstallationStatusData
	observed      []api.PluginObservedInstanceData
}

func (f *fakeApplier) ApplySnapshot(ctx context.Context, tenant string, instances []api.PluginDesiredInstanceData) ([]api.PluginApplyResultData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(instances))
	for _, in := range instances {
		ids = append(ids, tenant+"/"+in.InstanceID)
	}
	f.snapshots = append(f.snapshots, ids)
	if f.resultsErr != nil {
		return nil, f.resultsErr
	}
	if f.results != nil {
		return f.results, nil
	}
	out := make([]api.PluginApplyResultData, 0, len(instances))
	for _, in := range instances {
		out = append(out, api.PluginApplyResultData{InstanceID: in.InstanceID, Status: api.PluginAckApplied, Detail: "enabled"})
	}
	return out, nil
}

func (f *fakeApplier) Observe(context.Context, string) ([]api.PluginInstallationStatusData, []api.PluginObservedInstanceData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installations, f.observed, nil
}

func (f *fakeApplier) applyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.snapshots)
}

func newTestSyncer(t *testing.T, applier plugincontrol.Applier) *plugincontrol.Syncer {
	t.Helper()
	s, err := plugincontrol.NewSyncer(plugincontrol.SyncOptions{
		Tenant: "tenant-a", CachePath: filepath.Join(t.TempDir(), "applied.json"), Applier: applier,
	})
	if err != nil {
		t.Fatalf("NewSyncer: %v", err)
	}
	return s
}

func pluginDesiredData(revision uint64, digest string, ids ...string) api.PluginDesiredData {
	instances := make([]api.PluginDesiredInstanceData, 0, len(ids))
	for _, id := range ids {
		instances = append(instances, api.PluginDesiredInstanceData{
			InstanceID: id, PluginID: "io.test.plugin", Version: "0.1.0",
			Enabled: true, Isolation: plugincontrol.IsolationShared,
		})
	}
	return api.PluginDesiredData{Revision: revision, SnapshotDigest: digest, Instances: instances}
}

func (r *edgeRecorder) sendPluginDesired(t *testing.T, d api.PluginDesiredData) {
	t.Helper()
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	r.send(t, api.Envelope{V: api.Version, Type: api.MsgPluginDesired, Ts: time.Now().Unix(), Data: data})
}

// waitPluginAck 等待指定 revision 的 plugin_ack（可要求具体 status）。
func (r *edgeRecorder) waitPluginAck(t *testing.T, revision uint64, wantStatus string) api.PluginAckData {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range r.all() {
			if e.Type != api.MsgPluginAck {
				continue
			}
			var ack api.PluginAckData
			if err := json.Unmarshal(e.Data, &ack); err != nil || ack.Revision != revision {
				continue
			}
			if wantStatus != "" && ack.Status != wantStatus {
				continue
			}
			return ack
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("未收到 revision=%d status=%q 的 plugin_ack", revision, wantStatus)
	return api.PluginAckData{}
}

// waitPluginStatus 等待 sequence >= minSeq 的 plugin_status。
func (r *edgeRecorder) waitPluginStatus(t *testing.T, minSeq uint64) api.PluginStatusData {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range r.all() {
			if e.Type != api.MsgPluginStatus {
				continue
			}
			var st api.PluginStatusData
			if err := json.Unmarshal(e.Data, &st); err != nil || st.Sequence < minSeq {
				continue
			}
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("未收到 sequence>=%d 的 plugin_status", minSeq)
	return api.PluginStatusData{}
}

// waitPluginStatusWhere 等待满足谓词的 plugin_status（扫描全部已收信封，
// 因此拿到的是「满足条件的那一条」，而不是首条）。
func (r *edgeRecorder) waitPluginStatusWhere(t *testing.T, cond func(api.PluginStatusData) bool) api.PluginStatusData {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last api.PluginStatusData
	for time.Now().Before(deadline) {
		for _, e := range r.all() {
			if e.Type != api.MsgPluginStatus {
				continue
			}
			var st api.PluginStatusData
			if err := json.Unmarshal(e.Data, &st); err != nil {
				continue
			}
			last = st
			if cond(st) {
				return st
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("未收到满足条件的 plugin_status（最后一条 %+v）", last)
	return last
}

func pluginConfig(url string) *Config {
	return &Config{
		Server: url, EdgeID: "e-plugin",
		PollIntervalS: 3600, SyncIntervalS: 3600, ReportIntervalS: 30,
		Devices: []DeviceCfg{{ID: "d1", Adapter: "demo", Extra: map[string]string{"tick_interval_s": "3600"}}},
	}
}

// TestPluginDesiredAckedAndStatusAdvanced 是插件控制面 WS 闭环：
// 收 plugin_desired → 应用 → 回 plugin_ack(applied) → 立即重报 plugin_status。
func TestPluginDesiredAckedAndStatusAdvanced(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	applier := &fakeApplier{}
	syncer := newTestSyncer(t, applier)
	runEdge(t, pluginConfig(rec.url("ws")), WithPluginSync(syncer))

	first := rec.waitPluginStatus(t, 1)
	if first.BootID == "" || first.BootID != syncer.BootID() {
		t.Fatalf("上线即应上报 plugin_status（boot_id=%q，syncer=%q）", first.BootID, syncer.BootID())
	}
	if first.AppliedRevision != 0 {
		t.Fatalf("初始 applied_revision = %d, want 0", first.AppliedRevision)
	}

	rec.sendPluginDesired(t, pluginDesiredData(4, "digest-4", "i1", "i2"))
	ack := rec.waitPluginAck(t, 4, api.PluginAckApplied)
	if ack.SnapshotDigest != "digest-4" {
		t.Fatalf("ack 摘要 = %q, want digest-4", ack.SnapshotDigest)
	}
	if len(ack.Results) != 2 {
		t.Fatalf("ack 必须逐实例回报: %+v", ack.Results)
	}
	if applier.applyCount() != 1 {
		t.Fatalf("apply 调用数 = %d, want 1", applier.applyCount())
	}
	// 应用后必须立即重报实际态（不等下一拍心跳）。
	after := rec.waitPluginStatus(t, first.Sequence+1)
	if after.AppliedRevision != 4 {
		t.Fatalf("plugin_status.applied_revision = %d, want 4", after.AppliedRevision)
	}
	if after.BootID != first.BootID {
		t.Fatalf("同一进程内 boot_id 必须稳定: %q -> %q", first.BootID, after.BootID)
	}
	if after.Sequence <= first.Sequence {
		t.Fatalf("sequence 必须单调递增: %d -> %d", first.Sequence, after.Sequence)
	}
}

// TestPluginSameRevisionDifferentDigestRejectedOverWS 是暗卷 #1 的端到端证明。
func TestPluginSameRevisionDifferentDigestRejectedOverWS(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	applier := &fakeApplier{}
	syncer := newTestSyncer(t, applier)
	runEdge(t, pluginConfig(rec.url("ws")), WithPluginSync(syncer))
	rec.waitPluginStatus(t, 1)

	rec.sendPluginDesired(t, pluginDesiredData(2, "digest-A", "i1"))
	rec.waitPluginAck(t, 2, api.PluginAckApplied)
	applied := applier.applyCount()

	rec.sendPluginDesired(t, pluginDesiredData(2, "digest-B", "i1", "evil"))
	ack := rec.waitPluginAck(t, 2, api.PluginAckRejected)
	if strings.TrimSpace(ack.Results[0].Detail) == "" {
		t.Fatalf("rejected ack 必须带可读原因: %+v", ack)
	}
	if applier.applyCount() != applied {
		t.Fatal("被拒绝的快照不得触发 apply")
	}
	if syncer.AppliedRevision() != 2 {
		t.Fatalf("applied revision = %d, want 2（不得被异摘要快照改动）", syncer.AppliedRevision())
	}
	st := rec.waitPluginStatusWhere(t, func(s api.PluginStatusData) bool { return s.AppliedRevision == 2 })
	if st.Sequence <= 1 {
		t.Fatalf("拒绝后仍应有真实上报: %+v", st)
	}
}

// TestPluginOldRevisionRejectedOverWS 验证迟到/倒序快照不得让本地状态倒退。
func TestPluginOldRevisionRejectedOverWS(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	applier := &fakeApplier{}
	syncer := newTestSyncer(t, applier)
	runEdge(t, pluginConfig(rec.url("ws")), WithPluginSync(syncer))
	rec.waitPluginStatus(t, 1)

	rec.sendPluginDesired(t, pluginDesiredData(9, "digest-9", "i1"))
	rec.waitPluginAck(t, 9, api.PluginAckApplied)
	rec.sendPluginDesired(t, pluginDesiredData(5, "digest-5", "stale"))
	rec.waitPluginAck(t, 5, api.PluginAckRejected)
	if syncer.AppliedRevision() != 9 {
		t.Fatalf("applied revision 倒退: %d, want 9", syncer.AppliedRevision())
	}
}

// TestPluginStatusReReportedAfterReconnect 验证断线重连后插件面同样恢复：
// 同一进程 boot_id 不变、sequence 递增、applied_revision 保持（离线期间继续按
// 最后一个完整 applied revision 运行）。
func TestPluginStatusReReportedAfterReconnect(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	applier := &fakeApplier{}
	syncer := newTestSyncer(t, applier)
	runEdge(t, pluginConfig(rec.url("ws")), WithPluginSync(syncer))

	first := rec.waitPluginStatus(t, 1)
	rec.sendPluginDesired(t, pluginDesiredData(3, "digest-3", "i1"))
	rec.waitPluginAck(t, 3, api.PluginAckApplied)

	rec.dropCurrent()
	rec.waitHello(t, 2)
	after := rec.waitPluginStatus(t, first.Sequence+1)
	if after.BootID != first.BootID {
		t.Fatalf("重连后 boot_id 变了（同进程不得换 boot）: %q -> %q", first.BootID, after.BootID)
	}
	if after.Sequence <= first.Sequence {
		t.Fatalf("重连后 sequence 必须继续单调: %d -> %d", first.Sequence, after.Sequence)
	}
	if after.AppliedRevision != 3 {
		t.Fatalf("重连后 applied_revision = %d, want 3（必须继续按最后完整 revision 运行）", after.AppliedRevision)
	}
}

// TestPluginStatusPayloadIsRedacted 锁定上报红线在真实 WS 出口处成立。
func TestPluginStatusPayloadIsRedacted(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	applier := &fakeApplier{
		installations: []api.PluginInstallationStatusData{{PluginID: "io.test.plugin", Version: "0.1.0", Kind: "Driver"}},
		observed: []api.PluginObservedInstanceData{{
			InstanceID: "i1", PluginID: "io.test.plugin", Version: "0.1.0", HostOnline: true,
			State: "CRASHED", Health: "UNHEALTHY",
			Detail: "crash at C:\\Users\\example\\plugins.d\\p token=abc123\nstderr: panic: boom",
		}},
	}
	syncer := newTestSyncer(t, applier)
	runEdge(t, pluginConfig(rec.url("ws")), WithPluginSync(syncer))

	st := rec.waitPluginStatus(t, 1)
	if len(st.ObservedInstances) != 1 {
		t.Fatalf("observed = %+v", st.ObservedInstances)
	}
	detail := st.ObservedInstances[0].Detail
	for _, leak := range []string{`C:\Users`, "abc123", "\n", "example", "panic: boom"} {
		if strings.Contains(detail, leak) {
			t.Errorf("plugin_status 出网泄漏 %q: %q", leak, detail)
		}
	}
	// 整条信封原文再扫一遍（安装物也不得带本机路径）。
	for _, e := range rec.all() {
		if e.Type != api.MsgPluginStatus {
			continue
		}
		raw := string(e.Data)
		for _, leak := range []string{`C:\`, "/Users/", "abc123"} {
			if strings.Contains(raw, leak) {
				t.Errorf("plugin_status 信封泄漏 %q: %s", leak, raw)
			}
		}
	}
}

// TestPluginDesiredIgnoredWhenPlaneDisabled 验证未承载插件面的 Edge：
// 忽略 plugin_desired 并记 debug，绝不断开连接（§4 向后兼容要求），
// 设备面照常工作。
func TestPluginDesiredIgnoredWhenPlaneDisabled(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := pluginConfig(rec.url("ws"))
	runEdge(t, cfg) // 不注入 WithPluginSync

	key := api.DeviceKey("e-plugin", "d1")
	rec.waitState(t, key, func(st api.StateData) bool { return st.Online })
	rec.sendPluginDesired(t, pluginDesiredData(1, "digest-1", "i1"))

	// 不得回 ack、不得上报 plugin_status，但连接必须活着（设备命令仍可用）。
	time.Sleep(500 * time.Millisecond)
	if n := rec.count(func(e api.Envelope) bool { return e.Type == api.MsgPluginAck }); n != 0 {
		t.Fatalf("未启用插件面却回了 %d 条 plugin_ack", n)
	}
	if n := rec.count(func(e api.Envelope) bool { return e.Type == api.MsgPluginStatus }); n != 0 {
		t.Fatalf("未启用插件面却上报了 %d 条 plugin_status", n)
	}
	if rec.connections() != 1 {
		t.Fatalf("连接数 = %d, want 1（未知/不支持消息不得导致断连）", rec.connections())
	}
	rec.sendCommand(t, key, 501, "ping", "")
	if ack := rec.waitAck(t, 501); ack.Status != "ok" {
		t.Fatalf("设备面应照常工作: %+v", ack)
	}
}
