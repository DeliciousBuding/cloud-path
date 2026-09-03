package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// mkTenant 建测试租户并返回 id（tenant 表由 v3 建、v4 保证 default 存在）。
func mkTenant(t *testing.T, s *Store, slug string) int64 {
	t.Helper()
	id, err := s.CreateTenant(slug, slug)
	if err != nil {
		t.Fatalf("create tenant %s: %v", slug, err)
	}
	return id
}

// desiredRow 构造一条合法期望态输入：config 只含非敏感标量与 secret:// handle。
func desiredRow(tid int64, edge, inst, plugin string) PluginInstanceRow {
	return PluginInstanceRow{
		TenantID:   tid,
		EdgeID:     edge,
		InstanceID: inst,
		PluginID:   plugin,
		Version:    "1.0.0",
		Enabled:    true,
		Isolation:  "process",
		ConfigJSON: `{"interval":"30s","token":"secret://api_token"}`,
		SecretRefs: `["api_token"]`,
	}
}

func mustCreate(t *testing.T, s *Store, row PluginInstanceRow) uint64 {
	t.Helper()
	rev, err := s.CreatePluginInstance(row)
	if err != nil {
		t.Fatalf("create %s/%s: %v", row.EdgeID, row.InstanceID, err)
	}
	return rev
}

// TestPluginCreateRevisionMonotonic 锁定：Create 返回单调递增 revision，且 revision 是
// per-(tenant,edge) 游标——另一条 edge 从 1 重新开始，互不干扰。
func TestPluginCreateRevisionMonotonic(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "rev")

	var got []uint64
	for i := 1; i <= 3; i++ {
		got = append(got, mustCreate(t, s, desiredRow(tid, "e1", fmt.Sprintf("i%d", i), "p1")))
	}
	want := []uint64{1, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("revisions = %v, want %v", got, want)
		}
	}
	rev, err := s.PluginDesiredRevision(tid, "e1")
	if err != nil || rev != 3 {
		t.Fatalf("desired revision = %d err=%v, want 3", rev, err)
	}
	// 行内 revision = 该行写入时的 edge desired revision
	row, ok, err := s.GetPluginInstance(tid, "e1", "i2")
	if err != nil || !ok {
		t.Fatalf("get i2: ok=%v err=%v", ok, err)
	}
	if row.Revision != 2 {
		t.Fatalf("row.Revision = %d, want 2", row.Revision)
	}
	if row.PluginID != "p1" || !row.Enabled || row.Isolation != "process" {
		t.Fatalf("row fields = %+v", row)
	}
	if row.ConfigJSON != `{"interval":"30s","token":"secret://api_token"}` || row.SecretRefs != `["api_token"]` {
		t.Fatalf("payload = %q / %q", row.ConfigJSON, row.SecretRefs)
	}
	// 第二条 edge 独立计数
	if r := mustCreate(t, s, desiredRow(tid, "e2", "i1", "p1")); r != 1 {
		t.Fatalf("edge e2 first revision = %d, want 1", r)
	}
	if rev, _ := s.PluginDesiredRevision(tid, "e1"); rev != 3 {
		t.Fatalf("e1 revision drifted to %d after e2 write", rev)
	}
}

// TestPluginRevisionDenseUnderConcurrency 锁定暗卷：并发写不产生 revision 空洞或重复。
// 混合 create/update/delete 并发跑，最终 desired_revision 必须恰等于成功写入次数，
// 且返回的 revision 集合恰为 1..N（每个 revision 被消费且只被消费一次）。
func TestPluginRevisionDenseUnderConcurrency(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "dense")

	const creators = 12
	var wg sync.WaitGroup
	revCh := make(chan uint64, creators*3)
	errCh := make(chan error, creators*3)
	start := make(chan struct{})

	for i := 0; i < creators; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inst := fmt.Sprintf("i%02d", i)
			<-start
			rev, err := s.CreatePluginInstance(desiredRow(tid, "e1", inst, "p1"))
			if err != nil {
				errCh <- fmt.Errorf("create %s: %w", inst, err)
				return
			}
			revCh <- rev
			// 同一实例再 update + delete 之外的第二轮 update：同样必须消费一个新 revision
			up := desiredRow(tid, "e1", inst, "p1")
			up.Version = "2.0.0"
			rev2, err := s.UpdatePluginInstance(up)
			if err != nil {
				errCh <- fmt.Errorf("update %s: %w", inst, err)
				return
			}
			revCh <- rev2
		}(i)
	}
	close(start)
	wg.Wait()
	close(revCh)
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	var revs []uint64
	for r := range revCh {
		revs = append(revs, r)
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i] < revs[j] })
	total := uint64(len(revs))
	if total != creators*2 {
		t.Fatalf("成功写入 %d 次, want %d", total, creators*2)
	}
	for i, r := range revs {
		if r != uint64(i)+1 {
			t.Fatalf("revision 集合有空洞/重复: %v（第 %d 项 = %d, want %d）", revs, i, r, uint64(i)+1)
		}
	}
	cur, err := s.PluginDesiredRevision(tid, "e1")
	if err != nil || cur != total {
		t.Fatalf("desired_revision = %d err=%v, want %d", cur, err, total)
	}
	n, err := s.CountPluginInstances(tid)
	if err != nil || n != creators {
		t.Fatalf("count = %d err=%v, want %d", n, err, creators)
	}
}

// TestListPluginInstancesTenantIsolation 锁定：只返回本租户；tenantID<=0 解析为 default
// 租户而不是「不过滤」；跨租户 Get 返回 not found。
func TestListPluginInstancesTenantIsolation(t *testing.T) {
	s := openTest(t)
	a := mkTenant(t, s, "iso-a")
	b := mkTenant(t, s, "iso-b")

	mustCreate(t, s, desiredRow(a, "e1", "i1", "p1"))
	mustCreate(t, s, desiredRow(a, "e1", "i2", "p1"))
	// edge_id 在本仓是全局绑定单一租户的身份（devices.id 是 "<edge>/<device>" 全局主键，
	// upsertDevicesTx 同样拒绝跨租户复用 edge_id），所以 B 必须用自己的 edge id。
	mustCreate(t, s, desiredRow(b, "eB", "i1", "p1")) // 同 instance 名，不同租户不同 edge

	gotA, err := s.ListPluginInstancesTenant(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotA) != 2 {
		t.Fatalf("tenant A 看到 %d 行, want 2: %+v", len(gotA), gotA)
	}
	for _, r := range gotA {
		if r.TenantID != a {
			t.Fatalf("跨租户泄漏: %+v", r)
		}
	}
	gotB, err := s.ListPluginInstancesTenant(b)
	if err != nil || len(gotB) != 1 || gotB[0].TenantID != b {
		t.Fatalf("tenant B = %+v err=%v, want 1 行", gotB, err)
	}
	// B 伪造 A 的 edge id 写期望态 → fail-closed，且 A/B 两侧都不被污染
	revBefore, _ := s.PluginDesiredRevision(a, "e1")
	if _, err := s.CreatePluginInstance(desiredRow(b, "e1", "forged", "p1")); !errors.Is(err, ErrEdgeTenantMismatch) {
		t.Fatalf("伪造 edge 写入 err = %v, want ErrEdgeTenantMismatch", err)
	}
	if _, ok, _ := s.GetPluginInstance(b, "e1", "forged"); ok {
		t.Fatal("伪造写入在 B 侧留下了行")
	}
	gotA2, _ := s.ListPluginInstancesTenant(a)
	if len(gotA2) != 2 {
		t.Fatalf("伪造写入污染了 A: %d 行, want 2", len(gotA2))
	}
	if rev, _ := s.PluginDesiredRevision(a, "e1"); rev != revBefore {
		t.Fatalf("伪造写入推进了 A 的 revision: %d -> %d", revBefore, rev)
	}
	if n, _ := s.CountPluginInstances(b); n != 1 {
		t.Fatalf("伪造写入给 B 增加了实例: %d, want 1", n)
	}
	// B 读 A 的行 → not found（不是权限错误，是不存在）
	if _, ok, err := s.GetPluginInstance(b, "e1", "i2"); err != nil || ok {
		t.Fatalf("B 读到 A 的实例: ok=%v err=%v", ok, err)
	}
	// default 租户（id 1）视角：0 解析成 default，绝不当作全量
	zero, err := s.ListPluginInstancesTenant(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(zero) != 0 {
		t.Fatalf("tenantID=0 返回 %d 行, want 0（必须解析为 default 租户而非不过滤）", len(zero))
	}
	def, err := s.ListPluginInstancesTenant(1)
	if err != nil || len(def) != 0 {
		t.Fatalf("default 租户 = %+v err=%v", def, err)
	}
}

// TestPluginUpdatePreservesTenantAndHistory 锁定：update 推进 revision、保留 created_at、
// 不改 tenant_id；plugin_id 传空=保留，传不同值=fail-closed 且不消费 revision。
func TestPluginUpdatePreservesTenantAndHistory(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "upd")
	mustCreate(t, s, desiredRow(tid, "e1", "i1", "p1"))
	before, ok, err := s.GetPluginInstance(tid, "e1", "i1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}

	up := PluginInstanceRow{
		TenantID: tid, EdgeID: "e1", InstanceID: "i1",
		Version: "2.0.0", Enabled: false, Isolation: "thread",
		ConfigJSON: `{"interval":"60s"}`, SecretRefs: `[]`,
	}
	rev, err := s.UpdatePluginInstance(up)
	if err != nil {
		t.Fatal(err)
	}
	if rev != 2 {
		t.Fatalf("update revision = %d, want 2", rev)
	}
	after, _, err := s.GetPluginInstance(tid, "e1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if after.CreatedAt != before.CreatedAt {
		t.Fatalf("created_at 被改写: %d -> %d", before.CreatedAt, after.CreatedAt)
	}
	if after.TenantID != tid {
		t.Fatalf("tenant_id 被改写: %d -> %d", tid, after.TenantID)
	}
	if after.PluginID != "p1" {
		t.Fatalf("plugin_id 未保留: %q", after.PluginID)
	}
	if after.Version != "2.0.0" || after.Enabled || after.Isolation != "thread" {
		t.Fatalf("字段未更新: %+v", after)
	}
	if after.ConfigJSON != `{"interval":"60s"}` || after.SecretRefs != `[]` {
		t.Fatalf("payload 未更新: %q / %q", after.ConfigJSON, after.SecretRefs)
	}

	// 改写 plugin_id = 换插件 = 新实例语义，必须拒绝且不消费 revision
	bad := up
	bad.PluginID = "p2"
	if _, err := s.UpdatePluginInstance(bad); !errors.Is(err, ErrPluginImmutableField) {
		t.Fatalf("err = %v, want ErrPluginImmutableField", err)
	}
	if rev, _ := s.PluginDesiredRevision(tid, "e1"); rev != 2 {
		t.Fatalf("被拒写入消费了 revision: %d, want 2", rev)
	}
	// 不存在 → not found，同样不消费 revision
	missing := up
	missing.InstanceID = "nope"
	if _, err := s.UpdatePluginInstance(missing); !errors.Is(err, ErrPluginInstanceNotFound) {
		t.Fatalf("err = %v, want ErrPluginInstanceNotFound", err)
	}
	if rev, _ := s.PluginDesiredRevision(tid, "e1"); rev != 2 {
		t.Fatalf("not-found 写入消费了 revision: %d, want 2", rev)
	}
}

// TestPluginCreateConflictAndValidation 锁定：重复 Create → conflict；身份缺失 →
// identity incomplete；config/secret_refs 形状非法 → invalid config；三者都不消费 revision、不留半状态。
func TestPluginCreateConflictAndValidation(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "conflict")
	mustCreate(t, s, desiredRow(tid, "e1", "i1", "p1"))

	if _, err := s.CreatePluginInstance(desiredRow(tid, "e1", "i1", "p1")); !errors.Is(err, ErrPluginInstanceConflict) {
		t.Fatalf("err = %v, want ErrPluginInstanceConflict", err)
	}
	cases := []struct {
		name string
		mut  func(*PluginInstanceRow)
		want error
	}{
		{"缺 edge_id", func(r *PluginInstanceRow) { r.EdgeID = "" }, ErrPluginIdentityIncomplete},
		{"缺 instance_id", func(r *PluginInstanceRow) { r.InstanceID = "" }, ErrPluginIdentityIncomplete},
		{"缺 plugin_id", func(r *PluginInstanceRow) { r.PluginID = "" }, ErrPluginIdentityIncomplete},
		{"config 非对象", func(r *PluginInstanceRow) { r.ConfigJSON = `["a"]` }, ErrPluginConfigInvalid},
		{"config 值非字符串", func(r *PluginInstanceRow) { r.ConfigJSON = `{"n":1}` }, ErrPluginConfigInvalid},
		{"config 嵌套对象", func(r *PluginInstanceRow) { r.ConfigJSON = `{"a":{"b":"c"}}` }, ErrPluginConfigInvalid},
		{"config 非法 JSON", func(r *PluginInstanceRow) { r.ConfigJSON = `{` }, ErrPluginConfigInvalid},
		{"secret_refs 非数组", func(r *PluginInstanceRow) { r.SecretRefs = `{"a":1}` }, ErrPluginConfigInvalid},
		{"secret_refs 空名称", func(r *PluginInstanceRow) { r.SecretRefs = `[""]` }, ErrPluginConfigInvalid},
	}
	for _, c := range cases {
		row := desiredRow(tid, "e1", "x-"+strings.ReplaceAll(c.name, " ", "_"), "p1")
		c.mut(&row)
		_, err := s.CreatePluginInstance(row)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if rev, _ := s.PluginDesiredRevision(tid, "e1"); rev != 1 {
		t.Fatalf("被拒写入消费了 revision: %d, want 1", rev)
	}
	n, _ := s.CountPluginInstances(tid)
	if n != 1 {
		t.Fatalf("count = %d, want 1（被拒写入不得留下半状态行）", n)
	}
	// 空 config/secret_refs 归一为 {} / []，保证读侧永远能反序列化
	rev := mustCreate(t, s, PluginInstanceRow{TenantID: tid, EdgeID: "e1", InstanceID: "empty", PluginID: "p1"})
	if rev != 2 {
		t.Fatalf("revision = %d, want 2", rev)
	}
	row, _, _ := s.GetPluginInstance(tid, "e1", "empty")
	if row.ConfigJSON != "{}" || row.SecretRefs != "[]" {
		t.Fatalf("归一化失败: %q / %q", row.ConfigJSON, row.SecretRefs)
	}
}

// TestPluginConfigCanonicalized 锁定：config key 按规范形式（有序）存储，
// 使同内容字节稳定——Server 侧 snapshot digest 依赖这一点。
func TestPluginConfigCanonicalized(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "canon")
	row := desiredRow(tid, "e1", "i1", "p1")
	row.ConfigJSON = `{"z":"1","a":"2","m":"3"}`
	mustCreate(t, s, row)
	got, _, err := s.GetPluginInstance(tid, "e1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigJSON != `{"a":"2","m":"3","z":"1"}` {
		t.Fatalf("config 未规范化: %q", got.ConfigJSON)
	}
}

// TestPluginDeleteBumpsRevisionKeepsAudit 锁定契约：删除期望态推进 revision（Edge 靠新
// revision 收敛「实例已移除」），但绝不删审计；purge 只清该实例 observed 投影，
// 不动其他实例投影，也不动 per-plugin 安装事实。
func TestPluginDeleteBumpsRevisionKeepsAudit(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "del")
	mustCreate(t, s, desiredRow(tid, "e1", "i1", "p1"))
	mustCreate(t, s, desiredRow(tid, "e1", "i2", "p1"))
	if err := s.InsertAuditEvent(AuditEvent{
		TenantID: tid, ActorType: "user", ActorID: 1, Action: "plugin.instance.created",
		TargetType: "plugin_instance", TargetID: "e1/i1", Outcome: "success",
	}); err != nil {
		t.Fatal(err)
	}
	// Edge 上报两个实例的 observed + 安装物
	if err := s.UpsertPluginObservations(tid, "e1", []api.PluginObservedInstanceData{
		{InstanceID: "i1", PluginID: "p1", State: "running", Health: "healthy"},
		{InstanceID: "i2", PluginID: "p1", State: "running", Health: "healthy"},
	}, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPluginInstallations(tid, "e1", []api.PluginInstallationStatusData{
		{PluginID: "p1", Version: "1.0.0", Kind: "driver"},
	}); err != nil {
		t.Fatal(err)
	}

	// 默认不 purge：只删期望态，observed 投影保留
	rev, err := s.DeletePluginInstance(tid, "e1", "i1", false)
	if err != nil {
		t.Fatal(err)
	}
	if rev != 3 {
		t.Fatalf("delete revision = %d, want 3", rev)
	}
	if _, ok, _ := s.GetPluginInstance(tid, "e1", "i1"); ok {
		t.Fatal("期望态未删除")
	}
	obs, _ := s.ListPluginObservationsTenant(tid)
	if len(obs) != 2 {
		t.Fatalf("非 purge 删除动了 observed 投影: %d 行, want 2", len(obs))
	}
	// 审计必须还在（删除期望态不删审计）
	audits, err := s.ListAuditEvents(tid, 0, "", 10)
	if err != nil || len(audits) != 1 {
		t.Fatalf("审计被删: %d 条 err=%v, want 1", len(audits), err)
	}

	// purge：删该实例 observed 投影，保留其他实例投影与安装事实
	if _, err := s.DeletePluginInstance(tid, "e1", "i2", true); err != nil {
		t.Fatal(err)
	}
	obs, _ = s.ListPluginObservationsTenant(tid)
	if len(obs) != 1 || obs[0].InstanceID != "i1" {
		t.Fatalf("purge 后 observed = %+v, want 只剩 i1", obs)
	}
	insts, _ := s.ListPluginInstallationsTenant(tid)
	if len(insts) != 1 {
		t.Fatalf("purge 误删安装事实: %d 行, want 1", len(insts))
	}
	if n, _ := s.CountPluginInstances(tid); n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
	if _, err := s.DeletePluginInstance(tid, "e1", "i1", false); !errors.Is(err, ErrPluginInstanceNotFound) {
		t.Fatalf("重复删除 err = %v, want ErrPluginInstanceNotFound", err)
	}
	if rev, _ := s.PluginDesiredRevision(tid, "e1"); rev != 4 {
		t.Fatalf("被拒删除消费了 revision: %d, want 4", rev)
	}
}

// TestPluginEdgeRevisionZeroRow 锁定：从未同步过的 (tenant,edge) 返回全零游标且无错误，
// 调用方无需处理 sql.ErrNoRows。
func TestPluginEdgeRevisionZeroRow(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "zero")
	row, err := s.GetPluginEdgeRevision(tid, "never")
	if err != nil {
		t.Fatal(err)
	}
	if row.TenantID != tid || row.EdgeID != "never" {
		t.Fatalf("零行未填坐标: %+v", row)
	}
	if row.DesiredRevision != 0 || row.AppliedRevision != 0 || row.LastSequence != 0 ||
		row.BootID != "" || row.LastReportAt != 0 || row.LastAckAt != 0 {
		t.Fatalf("零行不是全零: %+v", row)
	}
	rev, err := s.PluginDesiredRevision(tid, "never")
	if err != nil || rev != 0 {
		t.Fatalf("revision = %d err=%v, want 0", rev, err)
	}
}

// TestPluginBootSequenceGuards 锁定 control-plane-sync.md §4.1 + §8 与暗卷：
// 同 boot 序号严格递增；重复/倒序忽略；新 boot 可从 1 开始；旧 boot 迟到消息（哪怕序号更大）
// 在新 boot 上线后必须被忽略，且不得复活旧 boot 或改写其序号。
func TestPluginBootSequenceGuards(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "boot")

	report := func(boot string, seq uint64, at int64) {
		t.Helper()
		if err := s.SetPluginEdgeReport(tid, "e1", boot, seq, at); err != nil {
			t.Fatalf("report %s/%d: %v", boot, seq, err)
		}
	}
	cursor := func() PluginEdgeRevisionRow {
		t.Helper()
		c, err := s.GetPluginEdgeRevision(tid, "e1")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	assert := func(boot string, seq uint64, reportAt int64) {
		t.Helper()
		c := cursor()
		if c.BootID != boot || c.LastSequence != seq || c.LastReportAt != reportAt {
			t.Fatalf("cursor = %+v, want boot=%s seq=%d reportAt=%d", c, boot, seq, reportAt)
		}
	}

	report("bootA", 1, 100)
	assert("bootA", 1, 100)
	report("bootA", 2, 200)
	assert("bootA", 2, 200)
	// 重复序号 → 幂等忽略，时间戳也不得被改写
	report("bootA", 2, 999)
	assert("bootA", 2, 200)
	// 倒序 → 忽略
	report("bootA", 1, 999)
	assert("bootA", 2, 200)
	// 新 boot 从 1 开始 → 接受并轮换
	report("bootB", 1, 300)
	assert("bootB", 1, 300)
	// 暗卷：旧 boot_id 的更大 sequence 迟到 → 必须忽略，不得驱逐新 boot
	report("bootA", 99, 999)
	assert("bootB", 1, 300)
	// 新 boot 继续正常推进
	report("bootB", 2, 400)
	assert("bootB", 2, 400)
	// report 方向永不推进 applied / desired
	if c := cursor(); c.AppliedRevision != 0 || c.DesiredRevision != 0 {
		t.Fatalf("report 改写了 revision: %+v", c)
	}
	if err := s.SetPluginEdgeReport(tid, "e1", "", 1, 1); !errors.Is(err, ErrPluginIdentityIncomplete) {
		t.Fatalf("空 boot_id err = %v, want ErrPluginIdentityIncomplete", err)
	}
	if err := s.SetPluginEdgeReport(tid, "", "bootC", 1, 1); !errors.Is(err, ErrPluginIdentityIncomplete) {
		t.Fatalf("空 edge_id err = %v, want ErrPluginIdentityIncomplete", err)
	}
}

// TestPluginAppliedRevisionGuards 锁定：只有 ack 推进 applied_revision；applied 不回退、
// 不得超过 Server 下发过的 desired_revision（Edge 不能声称应用了不存在的 revision）。
func TestPluginAppliedRevisionGuards(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "applied")
	mustCreate(t, s, desiredRow(tid, "e1", "i1", "p1"))
	mustCreate(t, s, desiredRow(tid, "e1", "i2", "p1")) // desired = 2

	ack := func(boot string, seq, applied uint64, at int64) {
		t.Helper()
		if err := s.SetPluginEdgeApplied(tid, "e1", boot, seq, applied, at); err != nil {
			t.Fatalf("applied %s/%d/%d: %v", boot, seq, applied, err)
		}
	}
	cur := func() PluginEdgeRevisionRow {
		t.Helper()
		c, err := s.GetPluginEdgeRevision(tid, "e1")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	ack("bootA", 1, 2, 500)
	if c := cur(); c.AppliedRevision != 2 || c.LastAckAt != 500 || c.LastSequence != 1 {
		t.Fatalf("applied 未推进: %+v", c)
	}
	if c := cur(); c.DesiredRevision != 2 {
		t.Fatalf("ack 改写了 desired: %+v", c)
	}
	// 回退 → 忽略
	ack("bootA", 2, 1, 600)
	if c := cur(); c.AppliedRevision != 2 || c.LastAckAt != 500 {
		t.Fatalf("applied 回退被接受: %+v", c)
	}
	// 超过 desired → 忽略（Server 从未下发 revision 3）
	ack("bootA", 3, 3, 700)
	if c := cur(); c.AppliedRevision != 2 || c.LastAckAt != 500 || c.LastSequence != 1 {
		t.Fatalf("超过 desired 的 applied 被接受: %+v", c)
	}
	// 合法推进
	mustCreate(t, s, desiredRow(tid, "e1", "i3", "p1")) // desired = 3
	ack("bootA", 2, 3, 800)
	if c := cur(); c.AppliedRevision != 3 || c.LastAckAt != 800 || c.LastSequence != 2 {
		t.Fatalf("合法 applied 未推进: %+v", c)
	}
}

// TestPluginObservationsSnapshotReplace 锁定：投影是全量快照替换——本次未上报的实例必须消失
// （否则 UI 会把早已停止的实例渲染成 observed，违反暗卷「无 observed 必须显式呈现未上报/stale」），
// 且替换严格 scoped 到 (tenant,edge)，不影响同租户其他 edge。
func TestPluginObservationsSnapshotReplace(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "snap")

	if err := s.UpsertPluginObservations(tid, "e1", []api.PluginObservedInstanceData{
		{InstanceID: "i1", PluginID: "p1", Version: "1.0.0", HostOnline: true, State: "running", Health: "healthy", RestartCount: 2, MessageRate: 1.5},
		{InstanceID: "i2", PluginID: "p1", State: "stopped", Health: "unhealthy", Detail: "exit code 1"},
	}, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPluginObservations(tid, "e2", []api.PluginObservedInstanceData{
		{InstanceID: "x1", PluginID: "p2", State: "running", Health: "healthy"},
	}, 1000); err != nil {
		t.Fatal(err)
	}
	obs, err := s.ListPluginObservationsTenant(tid)
	if err != nil || len(obs) != 3 {
		t.Fatalf("observed = %d err=%v, want 3", len(obs), err)
	}
	for _, o := range obs {
		if o.TenantID != tid || o.ReportedAt != 1000 {
			t.Fatalf("投影坐标错误: %+v", o)
		}
	}
	// e1 只上报 i1 → i2 投影必须消失；e2 不受影响
	if err := s.UpsertPluginObservations(tid, "e1", []api.PluginObservedInstanceData{
		{InstanceID: "i1", PluginID: "p1", Version: "1.1.0", State: "running", Health: "degraded", RestartCount: 5},
	}, 2000); err != nil {
		t.Fatal(err)
	}
	obs, _ = s.ListPluginObservationsTenant(tid)
	if len(obs) != 2 {
		t.Fatalf("替换后 observed = %d 行, want 2 (e1/i1 + e2/x1): %+v", len(obs), obs)
	}
	var e1row PluginObservationRow
	for _, o := range obs {
		if o.EdgeID == "e1" {
			e1row = o
		}
	}
	if e1row.InstanceID != "i1" || e1row.Version != "1.1.0" || e1row.Health != "degraded" ||
		e1row.RestartCount != 5 || e1row.ReportedAt != 2000 {
		t.Fatalf("刷新未生效: %+v", e1row)
	}
	if e1row.HostOnline {
		t.Fatal("未上报的 host_online 残留为 true")
	}
	// 空快照 = 该 edge 无实例运行
	if err := s.UpsertPluginObservations(tid, "e1", nil, 3000); err != nil {
		t.Fatal(err)
	}
	obs, _ = s.ListPluginObservationsTenant(tid)
	if len(obs) != 1 || obs[0].EdgeID != "e2" {
		t.Fatalf("空快照后 observed = %+v, want 只剩 e2/x1", obs)
	}
	// 批内重复 instance_id 是歧义快照，必须拒绝且不破坏既有投影
	err = s.UpsertPluginObservations(tid, "e2", []api.PluginObservedInstanceData{
		{InstanceID: "x1"}, {InstanceID: "x1"},
	}, 4000)
	if !errors.Is(err, ErrPluginIdentityIncomplete) {
		t.Fatalf("重复 instance_id err = %v, want ErrPluginIdentityIncomplete", err)
	}
	obs, _ = s.ListPluginObservationsTenant(tid)
	if len(obs) != 1 {
		t.Fatalf("被拒上报破坏了投影: %+v", obs)
	}
	if err := s.UpsertPluginObservations(tid, "", []api.PluginObservedInstanceData{{InstanceID: "x"}}, 1); !errors.Is(err, ErrPluginIdentityIncomplete) {
		t.Fatalf("空 edge_id err = %v, want ErrPluginIdentityIncomplete", err)
	}
}

// TestPluginInstallationsRoundTrip 锁定：安装物投影的嵌套 manifest 字段（permissions /
// contributions / capabilities）经 JSON 往返后逐字段相等，且不丢 verified/publisher。
func TestPluginInstallationsRoundTrip(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "inst")
	in := []api.PluginInstallationStatusData{
		{
			PluginID: "p1", Version: "1.2.3", Kind: "driver", Protocol: 2,
			Digest:            "sha256:abc",
			TrustMode:         "pinned",
			Verified:          true,
			VerifiedPublisher: "acme",
			Permissions:       api.PluginPermissionsData{Secrets: []string{"api_token"}, Network: []string{"outbound"}},
			Contributions: api.PluginContributionsData{
				Drivers: []api.PluginDriverContributionData{{ID: "d1", Title: "Driver 1"}},
			},
			Capabilities: []string{"read", "write"},
		},
		{PluginID: "p2", Version: "0.1.0", Kind: "application"},
	}
	if err := s.UpsertPluginInstallations(tid, "e1", in); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListPluginInstallationsTenant(tid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("installations = %d, want 2", len(got))
	}
	if got[0].PluginID != "p1" {
		t.Fatalf("排序错误: %+v", got)
	}
	if got[0].Verified != true || got[0].VerifiedPublisher != "acme" || got[0].TrustMode != "pinned" ||
		got[0].Protocol != 2 || got[0].Digest != "sha256:abc" {
		t.Fatalf("标量字段往返失败: %+v", got[0])
	}
	if len(got[0].Permissions.Secrets) != 1 || got[0].Permissions.Secrets[0] != "api_token" ||
		len(got[0].Permissions.Network) != 1 || got[0].Permissions.Network[0] != "outbound" {
		t.Fatalf("permissions 往返失败: %+v", got[0].Permissions)
	}
	if len(got[0].Contributions.Drivers) != 1 || got[0].Contributions.Drivers[0].ID != "d1" ||
		got[0].Contributions.Drivers[0].Title != "Driver 1" {
		t.Fatalf("contributions 往返失败: %+v", got[0].Contributions)
	}
	if len(got[0].Capabilities) != 2 || got[0].Capabilities[1] != "write" {
		t.Fatalf("capabilities 往返失败: %+v", got[0].Capabilities)
	}
	if got[0].TenantID != tid || got[0].EdgeID != "e1" || got[0].ReportedAt <= 0 {
		t.Fatalf("投影坐标错误: %+v", got[0])
	}
	// nil capabilities 归一为 []，读回不得是 null 语义
	if got[1].Capabilities != nil && len(got[1].Capabilities) != 0 {
		t.Fatalf("空 capabilities = %+v", got[1].Capabilities)
	}
	// 全量快照替换：只上报 p2 → p1 消失
	if err := s.UpsertPluginInstallations(tid, "e1", in[1:]); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListPluginInstallationsTenant(tid)
	if len(got) != 1 || got[0].PluginID != "p2" {
		t.Fatalf("替换失败: %+v", got)
	}
	if err := s.UpsertPluginInstallations(tid, "e1", []api.PluginInstallationStatusData{{PluginID: ""}}); !errors.Is(err, ErrPluginIdentityIncomplete) {
		t.Fatalf("空 plugin_id err = %v, want ErrPluginIdentityIncomplete", err)
	}
	if err := s.UpsertPluginInstallations(tid, "e1", []api.PluginInstallationStatusData{{PluginID: "p"}, {PluginID: "p"}}); !errors.Is(err, ErrPluginIdentityIncomplete) {
		t.Fatalf("重复 plugin_id err = %v, want ErrPluginIdentityIncomplete", err)
	}
}
