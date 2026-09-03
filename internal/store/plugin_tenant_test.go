package store

import (
	"errors"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// TestPluginProjectionCrossTenantForgery 锁定 §3 契约「任何 upsert 不得修改既有行的 tenant_id」
// 与暗卷「Tenant B 伪造 Tenant A 的 edge id 上报 → 不得写入任何 A/B 投影，不得驱逐 A」。
// 覆盖三条 Edge→Server 写路径：observed 投影、安装物投影、boot/sequence 游标。
func TestPluginProjectionCrossTenantForgery(t *testing.T) {
	s := openTest(t)
	a := mkTenant(t, s, "forge-a")
	b := mkTenant(t, s, "forge-b")

	// A 合法拥有 edge "e1"：期望态 + observed 投影 + 安装物 + 已建立的 boot 游标
	mustCreate(t, s, desiredRow(a, "e1", "i1", "p1"))
	if err := s.UpsertPluginObservations(a, "e1", []api.PluginObservedInstanceData{
		{InstanceID: "i1", PluginID: "p1", State: "running", Health: "healthy"},
	}, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPluginInstallations(a, "e1", []api.PluginInstallationStatusData{
		{PluginID: "p1", Version: "1.0.0", Kind: "driver"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPluginEdgeReport(a, "e1", "bootA", 7, 1000); err != nil {
		t.Fatal(err)
	}
	beforeObs, err := s.ListPluginObservationsTenant(a)
	if err != nil || len(beforeObs) != 1 {
		t.Fatalf("A 初始投影 = %+v err=%v", beforeObs, err)
	}
	beforeInst, err := s.ListPluginInstallationsTenant(a)
	if err != nil || len(beforeInst) != 1 {
		t.Fatalf("A 初始安装物 = %+v err=%v", beforeInst, err)
	}

	// B 伪造 A 的 edge id 上报 observed → 必须被拒
	err = s.UpsertPluginObservations(b, "e1", []api.PluginObservedInstanceData{
		{InstanceID: "i1", PluginID: "evil", State: "running", Health: "healthy"},
		{InstanceID: "forged", PluginID: "evil", State: "running"},
	}, 2000)
	if !errors.Is(err, ErrEdgeTenantMismatch) {
		t.Fatalf("伪造 observed err = %v, want ErrEdgeTenantMismatch", err)
	}
	// B 伪造安装物 → 必须被拒
	if err := s.UpsertPluginInstallations(b, "e1", []api.PluginInstallationStatusData{
		{PluginID: "evil", Version: "6.6.6", Kind: "driver"},
	}); !errors.Is(err, ErrEdgeTenantMismatch) {
		t.Fatalf("伪造 installations err = %v, want ErrEdgeTenantMismatch", err)
	}
	// B 伪造 boot/sequence 游标 → 必须被拒（不得驱逐 A 的 boot 身份）
	if err := s.SetPluginEdgeReport(b, "e1", "bootB", 99, 2000); !errors.Is(err, ErrEdgeTenantMismatch) {
		t.Fatalf("伪造 report err = %v, want ErrEdgeTenantMismatch", err)
	}
	if err := s.SetPluginEdgeApplied(b, "e1", "bootB", 99, 1, 2000); !errors.Is(err, ErrEdgeTenantMismatch) {
		t.Fatalf("伪造 applied err = %v, want ErrEdgeTenantMismatch", err)
	}

	// A 侧投影一行未变：tenant_id / 内容 / 时间戳都不得被改写
	afterObs, err := s.ListPluginObservationsTenant(a)
	if err != nil || len(afterObs) != 1 {
		t.Fatalf("A 投影被污染: %+v err=%v", afterObs, err)
	}
	if afterObs[0].InstanceID != beforeObs[0].InstanceID || afterObs[0].PluginID != "p1" ||
		afterObs[0].ReportedAt != 1000 || afterObs[0].TenantID != a {
		t.Fatalf("A 投影被改写: before=%+v after=%+v", beforeObs[0], afterObs[0])
	}
	afterInst, err := s.ListPluginInstallationsTenant(a)
	if err != nil || len(afterInst) != 1 || afterInst[0].PluginID != "p1" || afterInst[0].Version != "1.0.0" {
		t.Fatalf("A 安装物被改写: %+v err=%v", afterInst, err)
	}
	// A 的 boot 游标未被驱逐
	cur, err := s.GetPluginEdgeRevision(a, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if cur.BootID != "bootA" || cur.LastSequence != 7 || cur.LastReportAt != 1000 {
		t.Fatalf("A 的游标被驱逐: %+v", cur)
	}
	// B 侧不得留下任何投影行（不污染 B 自己）
	if obs, _ := s.ListPluginObservationsTenant(b); len(obs) != 0 {
		t.Fatalf("B 侧留下 observed 投影: %+v", obs)
	}
	if inst, _ := s.ListPluginInstallationsTenant(b); len(inst) != 0 {
		t.Fatalf("B 侧留下安装物投影: %+v", inst)
	}
	if rev, _ := s.PluginDesiredRevision(b, "e1"); rev != 0 {
		t.Fatalf("伪造上报给 B 造出了 revision 游标: %d", rev)
	}
	// 直查 DB：edge "e1" 在所有投影表里只归属 A 一个租户
	for _, q := range []string{
		`SELECT COUNT(DISTINCT tenant_id) FROM plugin_observations WHERE edge_id='e1'`,
		`SELECT COUNT(DISTINCT tenant_id) FROM plugin_installations WHERE edge_id='e1'`,
		`SELECT COUNT(DISTINCT tenant_id) FROM plugin_edge_revisions WHERE edge_id='e1'`,
		`SELECT COUNT(DISTINCT tenant_id) FROM plugin_desired_instances WHERE edge_id='e1'`,
	} {
		var n int
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s = %d 个租户, want 1", q, n)
		}
	}
	// 表名取自固定字面量清单（非外部输入），值走参数绑定
	for _, table := range []string{"plugin_observations", "plugin_installations", "plugin_edge_revisions"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE tenant_id=?`, b).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("B 在 %s 留下 %d 行, want 0", table, n)
		}
	}

	// A 的绑定未被破坏：拒绝之后 A 仍能正常上报并推进
	if err := s.UpsertPluginObservations(a, "e1", []api.PluginObservedInstanceData{
		{InstanceID: "i1", PluginID: "p1", State: "running", Health: "degraded"},
	}, 3000); err != nil {
		t.Fatalf("A 被伪造攻击后无法继续上报: %v", err)
	}
	if err := s.SetPluginEdgeReport(a, "e1", "bootA", 8, 3000); err != nil {
		t.Fatalf("A 被伪造攻击后无法推进序号: %v", err)
	}
	obs, _ := s.ListPluginObservationsTenant(a)
	if len(obs) != 1 || obs[0].Health != "degraded" || obs[0].ReportedAt != 3000 {
		t.Fatalf("A 后续上报未生效: %+v", obs)
	}
	// B 用自己的 edge id 上报必须成功（守卫不得误伤合法第二租户）
	if err := s.UpsertPluginObservations(b, "eB", []api.PluginObservedInstanceData{
		{InstanceID: "b1", PluginID: "p9", State: "running", Health: "healthy"},
	}, 4000); err != nil {
		t.Fatalf("B 用自己的 edge 上报被误拒: %v", err)
	}
	obsB, _ := s.ListPluginObservationsTenant(b)
	if len(obsB) != 1 || obsB[0].EdgeID != "eB" || obsB[0].InstanceID != "b1" {
		t.Fatalf("B 投影 = %+v", obsB)
	}
	obsA, _ := s.ListPluginObservationsTenant(a)
	if len(obsA) != 1 || obsA[0].EdgeID != "e1" {
		t.Fatalf("B 合法写入影响了 A: %+v", obsA)
	}
}

// TestPluginObservationNeverMutatesExistingTenantID 是直白的 SQL 级反向验证：
// 既有 observed 行的 tenant_id 在任何后续 upsert 之后都必须原样不变（契约硬要求）。
func TestPluginObservationNeverMutatesExistingTenantID(t *testing.T) {
	s := openTest(t)
	a := mkTenant(t, s, "tid-a")
	if err := s.UpsertPluginObservations(a, "e1", []api.PluginObservedInstanceData{
		{InstanceID: "i1", PluginID: "p1", State: "running"},
	}, 1000); err != nil {
		t.Fatal(err)
	}
	tenantOf := func() int64 {
		t.Helper()
		var tid int64
		if err := s.db.QueryRow(
			`SELECT tenant_id FROM plugin_observations WHERE edge_id='e1' AND instance_id='i1'`).Scan(&tid); err != nil {
			t.Fatal(err)
		}
		return tid
	}
	if got := tenantOf(); got != a {
		t.Fatalf("初始 tenant_id = %d, want %d", got, a)
	}
	// 同租户重复 upsert 刷新内容，但归属不变
	for i := 0; i < 3; i++ {
		if err := s.UpsertPluginObservations(a, "e1", []api.PluginObservedInstanceData{
			{InstanceID: "i1", PluginID: "p1", State: "stopped", Health: "unhealthy"},
		}, int64(2000+i)); err != nil {
			t.Fatal(err)
		}
		if got := tenantOf(); got != a {
			t.Fatalf("第 %d 次 upsert 改写了 tenant_id: %d, want %d", i, got, a)
		}
	}
	obs, _ := s.ListPluginObservationsTenant(a)
	if len(obs) != 1 || obs[0].ReportedAt != 2002 || obs[0].Health != "unhealthy" {
		t.Fatalf("刷新未生效: %+v", obs)
	}
	// 安装物同理
	if err := s.UpsertPluginInstallations(a, "e1", []api.PluginInstallationStatusData{
		{PluginID: "p1", Version: "1.0.0"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPluginInstallations(a, "e1", []api.PluginInstallationStatusData{
		{PluginID: "p1", Version: "2.0.0"},
	}); err != nil {
		t.Fatal(err)
	}
	var instTid int64
	if err := s.db.QueryRow(`SELECT tenant_id FROM plugin_installations WHERE edge_id='e1' AND plugin_id='p1'`).
		Scan(&instTid); err != nil {
		t.Fatal(err)
	}
	if instTid != a {
		t.Fatalf("installations tenant_id = %d, want %d", instTid, a)
	}
}
