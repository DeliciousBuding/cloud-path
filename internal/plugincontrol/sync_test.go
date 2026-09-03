package plugincontrol_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
)

// fakeApplier 是 plugincontrol.Applier 的替身：记录每次应用调用，
// 并按注入的函数产出逐实例结果（任何测试都不启动真实插件进程）。
type fakeApplier struct {
	mu    sync.Mutex
	calls []fakeApplyCall

	results       func(instances []api.PluginDesiredInstanceData) ([]api.PluginApplyResultData, error)
	installations []api.PluginInstallationStatusData
	observed      []api.PluginObservedInstanceData
	observeErr    error

	// concurrency 探针：记录同时在 ApplySnapshot 内的最大数量。
	inside int
	maxIn  int
}

type fakeApplyCall struct {
	tenant string
	ids    []string
}

func (f *fakeApplier) ApplySnapshot(ctx context.Context, tenant string, instances []api.PluginDesiredInstanceData) ([]api.PluginApplyResultData, error) {
	f.mu.Lock()
	f.inside++
	if f.inside > f.maxIn {
		f.maxIn = f.inside
	}
	ids := make([]string, 0, len(instances))
	for _, in := range instances {
		ids = append(ids, in.InstanceID)
	}
	f.calls = append(f.calls, fakeApplyCall{tenant: tenant, ids: ids})
	fn := f.results
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inside--
		f.mu.Unlock()
	}()

	if fn != nil {
		return fn(instances)
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
	return f.installations, f.observed, f.observeErr
}

func (f *fakeApplier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeApplier) lastIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1].ids
}

func (f *fakeApplier) maxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxIn
}

func desired(revision uint64, digest string, ids ...string) api.PluginDesiredData {
	instances := make([]api.PluginDesiredInstanceData, 0, len(ids))
	for _, id := range ids {
		instances = append(instances, api.PluginDesiredInstanceData{
			InstanceID: id, PluginID: testPluginID, Version: "0.1.0",
			Enabled: true, Isolation: plugincontrol.IsolationShared,
		})
	}
	return api.PluginDesiredData{Revision: revision, SnapshotDigest: digest, Instances: instances}
}

func newTestSyncer(t *testing.T, applier plugincontrol.Applier) (*plugincontrol.Syncer, string) {
	t.Helper()
	cachePath := filepath.Join(t.TempDir(), "applied.json")
	s, err := plugincontrol.NewSyncer(plugincontrol.SyncOptions{
		Tenant: "tenant-a", CachePath: cachePath, Applier: applier,
	})
	if err != nil {
		t.Fatalf("NewSyncer: %v", err)
	}
	return s, cachePath
}

func resultOf(ack api.PluginAckData, id string) (api.PluginApplyResultData, bool) {
	for _, r := range ack.Results {
		if r.InstanceID == id {
			return r, true
		}
	}
	return api.PluginApplyResultData{}, false
}

// ---- revision 规则（control-plane-sync.md §2 不变量 2/3、§8）----

func TestNewRevisionAppliesAndAdvancesCache(t *testing.T) {
	applier := &fakeApplier{}
	s, cachePath := newTestSyncer(t, applier)

	ack := s.HandleDesired(context.Background(), desired(7, "digest-7", "i1", "i2"))
	if ack.Status != api.PluginAckApplied {
		t.Fatalf("status = %q, want applied（results %+v）", ack.Status, ack.Results)
	}
	if ack.Revision != 7 || ack.SnapshotDigest != "digest-7" {
		t.Fatalf("ack = %+v", ack)
	}
	if s.AppliedRevision() != 7 {
		t.Fatalf("applied revision = %d, want 7", s.AppliedRevision())
	}
	if len(ack.Results) != 2 {
		t.Fatalf("必须逐实例回报，got %+v", ack.Results)
	}
	if applier.callCount() != 1 {
		t.Fatalf("apply 调用数 = %d, want 1", applier.callCount())
	}
	if got := applier.lastIDs(); len(got) != 2 || got[0] != "i1" || got[1] != "i2" {
		t.Fatalf("apply 收到的实例 = %v", got)
	}
	// applied cache 必须落盘（离线/重启续跑的依据）。
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("applied cache 未落盘: %v", err)
	}
	var cache plugincontrol.AppliedCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("applied cache 不可解析: %v", err)
	}
	if cache.AppliedRevision != 7 || cache.SnapshotDigest != "digest-7" {
		t.Fatalf("applied cache = %+v", cache)
	}
	if len(cache.Results) != 2 {
		t.Fatalf("applied cache 应保存每实例结果: %+v", cache.Results)
	}
	// 缓存里不得出现 boot_id（boot_id 必须每次进程启动都换新）。
	if strings.Contains(string(data), "boot_id") || strings.Contains(string(data), s.BootID()) {
		t.Fatal("applied cache 不得保存 boot_id（会让旧 boot 复活）")
	}
}

func TestSameRevisionSameDigestIsIdempotent(t *testing.T) {
	applier := &fakeApplier{}
	s, _ := newTestSyncer(t, applier)
	first := s.HandleDesired(context.Background(), desired(3, "digest-3", "i1"))
	if first.Status != api.PluginAckApplied {
		t.Fatalf("首次应用失败: %+v", first)
	}
	calls := applier.callCount()

	replay := s.HandleDesired(context.Background(), desired(3, "digest-3", "i1"))
	if replay.Status != api.PluginAckApplied {
		t.Fatalf("同 revision 同摘要重放应幂等 ACK applied，got %q", replay.Status)
	}
	if applier.callCount() != calls {
		t.Fatalf("幂等重放不得再次 apply（副作用）：%d -> %d", calls, applier.callCount())
	}
	if s.AppliedRevision() != 3 {
		t.Fatalf("applied revision = %d, want 3", s.AppliedRevision())
	}
}

// TestSameRevisionDifferentDigestRejected 是暗卷 #1：同 revision 异 payload 必须拒绝，
// applied revision 不变，且绝不落到本地。
func TestSameRevisionDifferentDigestRejected(t *testing.T) {
	applier := &fakeApplier{}
	s, cachePath := newTestSyncer(t, applier)
	if ack := s.HandleDesired(context.Background(), desired(3, "digest-A", "i1")); ack.Status != api.PluginAckApplied {
		t.Fatalf("首次应用失败: %+v", ack)
	}
	calls := applier.callCount()

	ack := s.HandleDesired(context.Background(), desired(3, "digest-B", "i1", "evil"))
	if ack.Status != api.PluginAckRejected {
		t.Fatalf("同 revision 异摘要必须 rejected，got %q（results %+v）", ack.Status, ack.Results)
	}
	if s.AppliedRevision() != 3 {
		t.Fatalf("applied revision 被改动: %d, want 3", s.AppliedRevision())
	}
	if applier.callCount() != calls {
		t.Fatal("被拒绝的快照不得触发 apply")
	}
	if got := s.Cache().SnapshotDigest; got != "digest-A" {
		t.Fatalf("cache 摘要被改写: %q, want digest-A", got)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "digest-B") || strings.Contains(string(data), "evil") {
		t.Fatalf("被拒绝的快照内容不得落盘: %s", data)
	}
	if len(ack.Results) == 0 || strings.TrimSpace(ack.Results[0].Detail) == "" {
		t.Fatalf("rejected ack 必须带可读原因: %+v", ack.Results)
	}
}

// TestOldRevisionRejected 是暗卷「迟到消息不得让本地状态倒退」的 Edge 侧半边。
func TestOldRevisionRejected(t *testing.T) {
	applier := &fakeApplier{}
	s, _ := newTestSyncer(t, applier)
	s.HandleDesired(context.Background(), desired(9, "digest-9", "i1"))
	calls := applier.callCount()

	ack := s.HandleDesired(context.Background(), desired(4, "digest-4", "stale"))
	if ack.Status != api.PluginAckRejected {
		t.Fatalf("旧 revision 必须 rejected，got %q", ack.Status)
	}
	if s.AppliedRevision() != 9 {
		t.Fatalf("applied revision 倒退: %d, want 9", s.AppliedRevision())
	}
	if applier.callCount() != calls {
		t.Fatal("旧 revision 不得触发 apply")
	}
	if !strings.Contains(ack.Results[0].Detail, "stale_revision") {
		t.Fatalf("rejected 原因应可机读: %q", ack.Results[0].Detail)
	}
}

// TestEmptyDigestRejected 锁定协议 fail-closed：没有摘要就无法验证
// 「同 revision 同内容」，一律拒绝而不是宽松接受。
func TestEmptyDigestRejected(t *testing.T) {
	applier := &fakeApplier{}
	s, _ := newTestSyncer(t, applier)
	for _, rev := range []uint64{0, 1, 5} {
		ack := s.HandleDesired(context.Background(), desired(rev, "  ", "i1"))
		if ack.Status != api.PluginAckRejected {
			t.Fatalf("revision %d 空摘要必须 rejected，got %q", rev, ack.Status)
		}
	}
	if applier.callCount() != 0 {
		t.Fatal("空摘要不得触发 apply")
	}
	if s.AppliedRevision() != 0 {
		t.Fatalf("applied revision = %d, want 0", s.AppliedRevision())
	}
}

// TestFirstSnapshotAtRevisionZeroApplies 保证全新 Edge（applied=0）能接受
// Server 的初始快照（revision 可能是 0），不会把首次收敛误判成异摘要。
func TestFirstSnapshotAtRevisionZeroApplies(t *testing.T) {
	applier := &fakeApplier{}
	s, _ := newTestSyncer(t, applier)
	ack := s.HandleDesired(context.Background(), desired(0, "digest-0", "i1"))
	if ack.Status != api.PluginAckApplied {
		t.Fatalf("首次 revision=0 快照应被应用，got %q（%+v）", ack.Status, ack.Results)
	}
	if s.Cache().SnapshotDigest != "digest-0" {
		t.Fatalf("cache 摘要 = %q", s.Cache().SnapshotDigest)
	}
	// 此后同 revision 异摘要必须被拒（不再是「从未应用」状态）。
	if ack := s.HandleDesired(context.Background(), desired(0, "digest-0b", "i1")); ack.Status != api.PluginAckRejected {
		t.Fatalf("已应用 revision 0 后异摘要必须 rejected，got %q", ack.Status)
	}
}

// TestPartialFailureDoesNotAdvanceRevision 是暗卷「单实例应用失败 → 整个 revision
// 不确认」的实现证明，并验证下一次完整快照仍能收敛。
func TestPartialFailureDoesNotAdvanceRevision(t *testing.T) {
	applier := &fakeApplier{}
	s, cachePath := newTestSyncer(t, applier)
	if ack := s.HandleDesired(context.Background(), desired(2, "digest-2", "i1")); ack.Status != api.PluginAckApplied {
		t.Fatalf("首次应用失败: %+v", ack)
	}

	applier.results = func(instances []api.PluginDesiredInstanceData) ([]api.PluginApplyResultData, error) {
		out := make([]api.PluginApplyResultData, 0, len(instances))
		for _, in := range instances {
			if in.InstanceID == "bad" {
				out = append(out, api.PluginApplyResultData{InstanceID: in.InstanceID, Status: api.PluginAckFailed, Detail: "start failed"})
				continue
			}
			out = append(out, api.PluginApplyResultData{InstanceID: in.InstanceID, Status: api.PluginAckApplied})
		}
		return out, nil
	}
	ack := s.HandleDesired(context.Background(), desired(3, "digest-3", "good", "bad"))
	if ack.Status != api.PluginAckFailed {
		t.Fatalf("部分失败必须回 failed，got %q", ack.Status)
	}
	if s.AppliedRevision() != 2 {
		t.Fatalf("部分失败时 revision 不得前进: %d, want 2", s.AppliedRevision())
	}
	good, ok := resultOf(ack, "good")
	if !ok || good.Status != api.PluginAckApplied {
		t.Fatalf("必须逐实例报告成功项: %+v", ack.Results)
	}
	bad, ok := resultOf(ack, "bad")
	if !ok || bad.Status != api.PluginAckFailed || bad.Detail == "" {
		t.Fatalf("必须逐实例报告失败原因: %+v", ack.Results)
	}
	data, _ := os.ReadFile(cachePath)
	if strings.Contains(string(data), "digest-3") {
		t.Fatalf("失败的 revision 不得写进 applied cache: %s", data)
	}

	// 修好后的高 revision 仍能正常收敛。
	applier.results = nil
	if ack := s.HandleDesired(context.Background(), desired(4, "digest-4", "good", "bad")); ack.Status != api.PluginAckApplied {
		t.Fatalf("后续完整快照应成功: %+v", ack)
	}
	if s.AppliedRevision() != 4 {
		t.Fatalf("applied revision = %d, want 4", s.AppliedRevision())
	}
}

func TestApplierErrorFailsWholeSnapshot(t *testing.T) {
	applier := &fakeApplier{}
	applier.results = func([]api.PluginDesiredInstanceData) ([]api.PluginApplyResultData, error) {
		return nil, fmt.Errorf("installations unavailable: %w", os.ErrPermission)
	}
	s, _ := newTestSyncer(t, applier)
	ack := s.HandleDesired(context.Background(), desired(5, "digest-5", "i1", "i2"))
	if ack.Status != api.PluginAckFailed {
		t.Fatalf("applier 整体失败必须回 failed，got %q", ack.Status)
	}
	if s.AppliedRevision() != 0 {
		t.Fatalf("revision 不得前进: %d", s.AppliedRevision())
	}
	if len(ack.Results) != 2 {
		t.Fatalf("必须为每个期望实例产出一条 failed 结果: %+v", ack.Results)
	}
}

// TestMissingInstanceResultTreatedAsFailure 防止 Applier 少报结果被当成成功。
func TestMissingInstanceResultTreatedAsFailure(t *testing.T) {
	applier := &fakeApplier{}
	applier.results = func([]api.PluginDesiredInstanceData) ([]api.PluginApplyResultData, error) {
		return []api.PluginApplyResultData{{InstanceID: "i1", Status: api.PluginAckApplied}}, nil
	}
	s, _ := newTestSyncer(t, applier)
	ack := s.HandleDesired(context.Background(), desired(1, "digest-1", "i1", "i2"))
	if ack.Status != api.PluginAckFailed {
		t.Fatalf("缺结果必须视为失败，got %q", ack.Status)
	}
	missing, ok := resultOf(ack, "i2")
	if !ok || missing.Status != api.PluginAckFailed {
		t.Fatalf("缺结果的实例必须补一条 failed: %+v", ack.Results)
	}
	if s.AppliedRevision() != 0 {
		t.Fatalf("revision 不得前进: %d", s.AppliedRevision())
	}
}

// ---- 离线与重启（§8）----

// TestOfflineKeepsLastAppliedAndConvergesOnlyFinalSnapshot 是暗卷 #4：
// 断线期间 Server 改了两次期望态，重连后只收敛当前最终快照，不回放中间操作；
// 断线期间本地仍按最后一个完整 applied revision 运行。
func TestOfflineKeepsLastAppliedAndConvergesOnlyFinalSnapshot(t *testing.T) {
	applier := &fakeApplier{}
	s, _ := newTestSyncer(t, applier)
	if ack := s.HandleDesired(context.Background(), desired(3, "digest-3", "i1")); ack.Status != api.PluginAckApplied {
		t.Fatalf("首次应用失败: %+v", ack)
	}

	// 断线期间：不上报 desired，但本地实际态仍按 revision 3 可观测。
	for i := 0; i < 3; i++ {
		st := s.Status(context.Background())
		if st.AppliedRevision != 3 {
			t.Fatalf("断线期间 applied_revision = %d, want 3（必须继续按最后完整 revision 运行）", st.AppliedRevision)
		}
	}
	callsBefore := applier.callCount()

	// 重连：Server 只下发当前最终快照（revision 5，中间态 revision 4 从不出现）。
	ack := s.HandleDesired(context.Background(), desired(5, "digest-5", "i1", "i2"))
	if ack.Status != api.PluginAckApplied {
		t.Fatalf("重连收敛失败: %+v", ack)
	}
	if applier.callCount() != callsBefore+1 {
		t.Fatalf("只应应用一份最终快照，apply 调用 %d -> %d", callsBefore, applier.callCount())
	}
	// 断线期间被跳过的中间 revision 迟到到达 → 必须被拒（不倒退、不回放）。
	if late := s.HandleDesired(context.Background(), desired(4, "digest-4", "i1")); late.Status != api.PluginAckRejected {
		t.Fatalf("中间 revision 迟到必须 rejected，got %q", late.Status)
	}
	if s.AppliedRevision() != 5 {
		t.Fatalf("applied revision = %d, want 5", s.AppliedRevision())
	}
}

// TestRestartRestoresAppliedCacheAndNewBootID 是暗卷「Edge 重启 → 从本地 applied
// cache 启动 + 新 boot_id 上报」的实现证明。
func TestRestartRestoresAppliedCacheAndNewBootID(t *testing.T) {
	applier := &fakeApplier{}
	cachePath := filepath.Join(t.TempDir(), "applied.json")
	first, err := plugincontrol.NewSyncer(plugincontrol.SyncOptions{
		Tenant: "tenant-a", CachePath: cachePath, Applier: applier,
	})
	if err != nil {
		t.Fatal(err)
	}
	first.HandleDesired(context.Background(), desired(6, "digest-6", "i1"))
	oldBoot := first.BootID()

	// 进程重启：新 Syncer 读同一份 cache。
	second, err := plugincontrol.NewSyncer(plugincontrol.SyncOptions{
		Tenant: "tenant-a", CachePath: cachePath, Applier: applier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.AppliedRevision() != 6 {
		t.Fatalf("重启后 applied revision = %d, want 6（应从本地 cache 恢复）", second.AppliedRevision())
	}
	if second.BootID() == oldBoot || second.BootID() == "" {
		t.Fatalf("boot_id 必须每次进程启动都换新: old=%q new=%q", oldBoot, second.BootID())
	}
	st := second.Status(context.Background())
	if st.Sequence != 1 {
		t.Fatalf("新 boot 的 sequence 必须从 1 开始，got %d", st.Sequence)
	}
	// 旧 revision 迟到：不得让本地状态倒退。
	if ack := second.HandleDesired(context.Background(), desired(5, "digest-5", "i1")); ack.Status != api.PluginAckRejected {
		t.Fatalf("重启后旧 revision 必须 rejected，got %q", ack.Status)
	}
	// 同 revision 同摘要重放：幂等。
	if ack := second.HandleDesired(context.Background(), desired(6, "digest-6", "i1")); ack.Status != api.PluginAckApplied {
		t.Fatalf("重启后同 revision 同摘要应幂等 applied，got %q", ack.Status)
	}
}

func TestCorruptCacheFailsSafeFromZero(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "applied.json")
	if err := os.WriteFile(cachePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	applier := &fakeApplier{}
	s, err := plugincontrol.NewSyncer(plugincontrol.SyncOptions{
		Tenant: "tenant-a", CachePath: cachePath, Applier: applier,
	})
	if err != nil {
		t.Fatalf("cache 损坏不应阻止启动（声明式全量快照可收敛）: %v", err)
	}
	if s.AppliedRevision() != 0 {
		t.Fatalf("损坏 cache 必须从零开始，got %d", s.AppliedRevision())
	}
	if ack := s.HandleDesired(context.Background(), desired(2, "digest-2", "i1")); ack.Status != api.PluginAckApplied {
		t.Fatalf("从零开始应能应用 Server 当前快照: %+v", ack)
	}
	// 损坏文件必须已被健康内容覆盖（原子写）。
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var cache plugincontrol.AppliedCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("修复后的 cache 仍不可解析: %v (%s)", err, data)
	}
}

func TestLoadAppliedCacheMissingIsZero(t *testing.T) {
	cache, err := plugincontrol.LoadAppliedCache(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("缺失 cache 不是错误: %v", err)
	}
	if !cache.IsEmpty() {
		t.Fatalf("缺失 cache 应为零值: %+v", cache)
	}
	// 空路径 = 不持久化：同样零值无错，Save 也必须是 no-op。
	cache, err = plugincontrol.LoadAppliedCache("")
	if err != nil || !cache.IsEmpty() {
		t.Fatalf("空路径应返回零值 cache，got %+v err=%v", cache, err)
	}
	if err := plugincontrol.SaveAppliedCache("", plugincontrol.AppliedCache{AppliedRevision: 9}); err != nil {
		t.Fatalf("空路径 Save 应为 no-op: %v", err)
	}
}

// ---- plugin_status 上报 ----

func TestStatusBootIDAndMonotonicSequence(t *testing.T) {
	applier := &fakeApplier{}
	applier.installations = []api.PluginInstallationStatusData{{PluginID: testPluginID, Version: "0.1.0", Kind: "Driver"}}
	applier.observed = []api.PluginObservedInstanceData{{InstanceID: "i1", PluginID: testPluginID, Version: "0.1.0", HostOnline: true, State: "HEALTHY", Health: "HEALTHY"}}
	s, _ := newTestSyncer(t, applier)
	s.HandleDesired(context.Background(), desired(4, "digest-4", "i1"))

	var prev uint64
	for i := 0; i < 5; i++ {
		st := s.Status(context.Background())
		if st.BootID != s.BootID() || st.BootID == "" {
			t.Fatalf("boot_id 必须稳定且非空: %q", st.BootID)
		}
		if st.Sequence <= prev {
			t.Fatalf("sequence 必须单调递增: %d -> %d", prev, st.Sequence)
		}
		prev = st.Sequence
		if st.AppliedRevision != 4 {
			t.Fatalf("applied_revision = %d, want 4", st.AppliedRevision)
		}
		if len(st.Installations) != 1 || len(st.ObservedInstances) != 1 {
			t.Fatalf("status 应带安装物与实例实际态: %+v", st)
		}
	}
}

// TestStatusRedactsDetail 锁定上报脱敏红线：即使 Applier 失手给出含绝对路径与
// 凭据形态的 detail，出网前也必须被清洗。
func TestStatusRedactsDetail(t *testing.T) {
	applier := &fakeApplier{}
	applier.observed = []api.PluginObservedInstanceData{{
		InstanceID: "i1", PluginID: testPluginID, Version: "0.1.0", HostOnline: true,
		State: "CRASHED", Health: "UNHEALTHY",
		Detail: "exit status 1 at C:\\Users\\example\\plugins.d\\x token=abc123\nstderr: panic: boom",
	}}
	s, _ := newTestSyncer(t, applier)
	st := s.Status(context.Background())
	if len(st.ObservedInstances) != 1 {
		t.Fatalf("observed = %+v", st.ObservedInstances)
	}
	detail := st.ObservedInstances[0].Detail
	for _, leak := range []string{`C:\Users`, "abc123", "\n", "example"} {
		if strings.Contains(detail, leak) {
			t.Errorf("plugin_status detail 泄漏 %q: %q", leak, detail)
		}
	}
	if len(detail) > plugincontrol.DetailLimit {
		t.Errorf("detail 超长: %d", len(detail))
	}
}

func TestStatusSurvivesObserveError(t *testing.T) {
	applier := &fakeApplier{}
	applier.observeErr = fmt.Errorf("lockfile unreadable")
	s, _ := newTestSyncer(t, applier)
	st := s.Status(context.Background())
	if st.BootID == "" || st.Sequence != 1 {
		t.Fatalf("observe 失败也必须能上报 boot/sequence: %+v", st)
	}
}

// TestConcurrentDesiredIsSerialized 验证多份快照并发到达时严格串行：
// 读循环为每条消息各起协程，Syncer 必须自己保证不会交错写本地状态。
func TestConcurrentDesiredIsSerialized(t *testing.T) {
	applier := &fakeApplier{}
	applier.results = func(instances []api.PluginDesiredInstanceData) ([]api.PluginApplyResultData, error) {
		time.Sleep(5 * time.Millisecond) // 放大交错窗口
		out := make([]api.PluginApplyResultData, 0, len(instances))
		for _, in := range instances {
			out = append(out, api.PluginApplyResultData{InstanceID: in.InstanceID, Status: api.PluginAckApplied})
		}
		return out, nil
	}
	s, _ := newTestSyncer(t, applier)

	var wg sync.WaitGroup
	for i := 1; i <= 12; i++ {
		wg.Add(1)
		go func(rev uint64) {
			defer wg.Done()
			s.HandleDesired(context.Background(), desired(rev, fmt.Sprintf("digest-%d", rev), "i1"))
		}(uint64(i))
	}
	wg.Wait()

	if max := applier.maxConcurrent(); max != 1 {
		t.Fatalf("ApplySnapshot 并发数 = %d, want 1（必须串行）", max)
	}
	// 最终 applied revision 必须是「被接受的最大 revision」，且 cache 与之一致。
	applied := s.AppliedRevision()
	if applied != 12 {
		t.Fatalf("applied revision = %d, want 12", applied)
	}
	if got := s.Cache().SnapshotDigest; got != fmt.Sprintf("digest-%d", applied) {
		t.Fatalf("cache 摘要 = %q，与 applied revision %d 不一致", got, applied)
	}
}

func TestNewSyncerRequiresApplier(t *testing.T) {
	if _, err := plugincontrol.NewSyncer(plugincontrol.SyncOptions{}); err == nil {
		t.Fatal("缺少 Applier 必须报错")
	}
}
