package storeport

import (
	"errors"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

func row(tenant int64, edge, instance string) PluginInstanceRow {
	return PluginInstanceRow{
		TenantID: tenant, EdgeID: edge, InstanceID: instance,
		PluginID: "p1", Version: "1.0.0", Enabled: true, Isolation: "shared",
		ConfigJSON: `{"interval":"30"}`, SecretRefs: `["api_token"]`,
	}
}

// TestMemoryRevisionMonotonic 锁定 revision 语义：每次合法写 +1，失败不增。
func TestMemoryRevisionMonotonic(t *testing.T) {
	m := NewMemory()
	rev, err := m.CreatePluginInstance(row(1, "e1", "box1"))
	if err != nil || rev != 1 {
		t.Fatalf("create = %d err=%v, want 1", rev, err)
	}
	// 重复创建：冲突且不增 revision。
	if _, err := m.CreatePluginInstance(row(1, "e1", "box1")); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复创建 err = %v, want ErrConflict", err)
	}
	if got, _ := m.PluginDesiredRevision(1, "e1"); got != 1 {
		t.Fatalf("冲突后 revision = %d, want 1", got)
	}
	updated := row(1, "e1", "box1")
	updated.Version = "2.0.0"
	if rev, err = m.UpdatePluginInstance(updated); err != nil || rev != 2 {
		t.Fatalf("update = %d err=%v, want 2", rev, err)
	}
	if rev, err = m.DeletePluginInstance(1, "e1", "box1", false); err != nil || rev != 3 {
		t.Fatalf("delete = %d err=%v, want 3（删除同样要让 Edge 收敛）", rev, err)
	}
	if _, err := m.UpdatePluginInstance(updated); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新已删除行 err = %v, want ErrNotFound", err)
	}
	if _, err := m.DeletePluginInstance(1, "e1", "box1", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除已删除行 err = %v, want ErrNotFound", err)
	}
	if got, _ := m.PluginDesiredRevision(1, "e1"); got != 3 {
		t.Fatalf("最终 revision = %d, want 3", got)
	}
}

// TestMemoryTenantIsolation 锁定租户作用域：跨租户读不到、改不动、删不掉，
// 且任何写入都不会改写既有行的 tenant_id。
func TestMemoryTenantIsolation(t *testing.T) {
	m := NewMemory()
	if _, err := m.CreatePluginInstance(row(1, "e1", "box1")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreatePluginInstance(row(2, "e1", "box1")); err != nil {
		t.Fatal(err)
	}
	rows1, err := m.ListPluginInstancesTenant(1)
	if err != nil || len(rows1) != 1 || rows1[0].TenantID != 1 {
		t.Fatalf("tenant1 行 = %+v err=%v", rows1, err)
	}
	rows2, err := m.ListPluginInstancesTenant(2)
	if err != nil || len(rows2) != 1 || rows2[0].TenantID != 2 {
		t.Fatalf("tenant2 行 = %+v err=%v", rows2, err)
	}
	if _, ok, err := m.GetPluginInstance(2, "e1", "box1"); err != nil || !ok {
		t.Fatalf("tenant2 自己的行读不到: ok=%v err=%v", ok, err)
	}
	// 同 edge/instance 名在不同租户下是**不同**的行，revision 也各自独立。
	if rev1, _ := m.PluginDesiredRevision(1, "e1"); rev1 != 1 {
		t.Fatalf("tenant1 revision = %d", rev1)
	}
	if rev2, _ := m.PluginDesiredRevision(2, "e1"); rev2 != 1 {
		t.Fatalf("tenant2 revision = %d（应与 tenant1 各自独立计数）", rev2)
	}
	// 用 tenant1 身份去改 tenant2 独有的行：必须按不存在处理（fail-closed）。
	if _, err := m.CreatePluginInstance(row(2, "e1", "only2")); err != nil {
		t.Fatal(err)
	}
	foreign := row(1, "e1", "only2")
	if _, err := m.UpdatePluginInstance(foreign); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨租户改写 err = %v, want ErrNotFound", err)
	}
	if _, err := m.DeletePluginInstance(1, "e1", "only2", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨租户删除 err = %v, want ErrNotFound", err)
	}
	// 合法更新不得改写既有行的 tenant 归属。
	own := row(1, "e1", "box1")
	own.Version = "9.9.9"
	if _, err := m.UpdatePluginInstance(own); err != nil {
		t.Fatal(err)
	}
	got, ok, err := m.GetPluginInstance(1, "e1", "box1")
	if err != nil || !ok || got.TenantID != 1 || got.Version != "9.9.9" {
		t.Fatalf("更新后行 = %+v ok=%v err=%v", got, ok, err)
	}
	if n1, _ := m.CountPluginInstances(1); n1 != 1 {
		t.Fatalf("tenant1 计数 = %d, want 1（更新不得新增行）", n1)
	}
	rows2b, err := m.ListPluginInstancesTenant(2)
	if err != nil || len(rows2b) != 2 {
		t.Fatalf("tenant2 行被污染: %+v err=%v", rows2b, err)
	}
	for _, r := range rows2b {
		if r.TenantID != 2 {
			t.Fatalf("tenant2 行归属被改写: %+v", r)
		}
	}
}

// TestMemoryQuotaRejectsWithoutRevision 锁定暗卷 6 在存储层的一面：
// 配额拒绝不写入、不增 revision。
func TestMemoryQuotaRejectsWithoutRevision(t *testing.T) {
	m := NewMemory()
	if err := m.SetTenantPolicy(7, TenantPolicyRow{TenantID: 7, QuotaPluginInstances: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreatePluginInstance(row(7, "e1", "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreatePluginInstance(row(7, "e1", "b")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreatePluginInstance(row(7, "e1", "c")); !errors.Is(err, ErrQuota) {
		t.Fatalf("第三个实例 err = %v, want ErrQuota", err)
	}
	if n, _ := m.CountPluginInstances(7); n != 2 {
		t.Fatalf("配额拒绝后实例数 = %d, want 2", n)
	}
	if rev, _ := m.PluginDesiredRevision(7, "e1"); rev != 2 {
		t.Fatalf("配额拒绝后 revision = %d, want 2", rev)
	}
	// 其他租户不受该配额影响。
	if _, err := m.CreatePluginInstance(row(8, "e1", "x")); err != nil {
		t.Fatalf("tenant8 被 tenant7 的配额牵连: %v", err)
	}
	// 未设策略 = 本实现不设限（Server 侧仍按 tenantpolicy 默认值先判一次）。
	for i := 0; i < 5; i++ {
		if _, err := m.CreatePluginInstance(row(9, "e1", string(rune('a'+i)))); err != nil {
			t.Fatalf("无策略租户被拒绝: %v", err)
		}
	}
}

// TestMemoryPurgeSemantics 锁定 purge：默认保留 observed 投影，purge 才删。
func TestMemoryPurgeSemantics(t *testing.T) {
	m := NewMemory()
	if _, err := m.CreatePluginInstance(row(1, "e1", "keep")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreatePluginInstance(row(1, "e1", "gone")); err != nil {
		t.Fatal(err)
	}
	obs := []api.PluginObservedInstanceData{
		{InstanceID: "keep", PluginID: "p1", Version: "1.0.0", State: "HEALTHY", Health: "HEALTHY"},
		{InstanceID: "gone", PluginID: "p1", Version: "1.0.0", State: "CRASHED", Health: "UNKNOWN"},
	}
	if err := m.UpsertPluginObservations(1, "e1", obs, 1234); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeletePluginInstance(1, "e1", "keep", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeletePluginInstance(1, "e1", "gone", true); err != nil {
		t.Fatal(err)
	}
	rows, err := m.ListPluginObservationsTenant(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].InstanceID != "keep" || rows[0].ReportedAt != 1234 {
		t.Fatalf("purge 语义错误: %+v", rows)
	}
	if rows[0].State != "HEALTHY" || rows[0].Health != "HEALTHY" {
		t.Fatalf("observed 行还原错误: %+v", rows[0])
	}
	if got := rows[0].Observed(); got.InstanceID != "keep" || got.State != "HEALTHY" {
		t.Fatalf("Observed() 映射错误: %+v", got)
	}
}

// TestMemoryProjectionRoundtrip 锁定上报投影的整体替换语义与公开字段往返。
func TestMemoryProjectionRoundtrip(t *testing.T) {
	m := NewMemory()
	in := []api.PluginInstallationStatusData{{
		PluginID: "p1", Version: "1.0.0", Kind: "Driver", Protocol: 1,
		Digest: "sha256:aa", TrustMode: "verified", Verified: true, VerifiedPublisher: "acme",
		Permissions: api.PluginPermissionsData{Secrets: []string{"api_token"}, Hardware: []string{"serial"}},
		Contributions: api.PluginContributionsData{Drivers: []api.PluginDriverContributionData{
			{ID: "stcb", Title: "STC-B", Discovery: "manual"},
		}},
		Capabilities: []string{"cloudpath.dev/capability/clock@1"},
	}}
	if err := m.UpsertPluginInstallations(1, "e1", in); err != nil {
		t.Fatal(err)
	}
	rows, err := m.ListPluginInstallationsTenant(1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("installations = %+v err=%v", rows, err)
	}
	got := rows[0].Status()
	if got.PluginID != "p1" || got.VerifiedPublisher != "acme" || len(got.Permissions.Secrets) != 1 ||
		len(got.Contributions.Drivers) != 1 || len(got.Capabilities) != 1 {
		t.Fatalf("安装物投影往返丢字段: %+v", got)
	}
	// 整体替换：新上报覆盖旧集合，不做合并。
	if err := m.UpsertPluginInstallations(1, "e1", []api.PluginInstallationStatusData{{PluginID: "p2", Version: "2.0.0"}}); err != nil {
		t.Fatal(err)
	}
	rows, _ = m.ListPluginInstallationsTenant(1)
	if len(rows) != 1 || rows[0].PluginID != "p2" {
		t.Fatalf("上报未整体替换: %+v", rows)
	}
	// 其他租户读不到。
	if rows, err := m.ListPluginInstallationsTenant(2); err != nil || len(rows) != 0 {
		t.Fatalf("跨租户安装物泄漏: %+v err=%v", rows, err)
	}
}

// TestMemoryEdgeRevisionProjection 锁定 revision/applied/boot/sequence 投影往返。
func TestMemoryEdgeRevisionProjection(t *testing.T) {
	m := NewMemory()
	if _, err := m.CreatePluginInstance(row(1, "e1", "box1")); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPluginEdgeReport(1, "e1", "boot-1", 3, 1000); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetPluginEdgeRevision(1, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if got.BootID != "boot-1" || got.LastSequence != 3 || got.LastReportAt != 1000 ||
		got.DesiredRevision != 1 || got.AppliedRevision != 0 {
		t.Fatalf("report 投影错误: %+v", got)
	}
	if err := m.SetPluginEdgeApplied(1, "e1", "boot-1", 4, 1, 2000); err != nil {
		t.Fatal(err)
	}
	got, err = m.GetPluginEdgeRevision(1, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AppliedRevision != 1 || got.LastAckAt != 2000 || got.LastSequence != 4 {
		t.Fatalf("applied 投影错误: %+v", got)
	}
	// 未记录的 edge 返回零值行而不是错误（读面据此判定「从未上报」）。
	if other, err := m.GetPluginEdgeRevision(1, "eX"); err != nil || other.LastReportAt != 0 {
		t.Fatalf("未知 edge 投影 = %+v err=%v", other, err)
	}
}
