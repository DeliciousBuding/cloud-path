package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/tenantpolicy"
)

// TestTenantPolicyDefaultsWhenAbsent 锁定：无策略行 = 全 0 = 全部继承默认，
// 且 Resolve() 恰等于 tenantpolicy.Defaults()（store 不自带第二份默认值副本）。
// 0 永远不是「无限」：解析后每个上限与天数都是正数。
func TestTenantPolicyDefaultsWhenAbsent(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "pol-absent")

	row, err := s.GetTenantPolicy(tid)
	if err != nil {
		t.Fatal(err)
	}
	if row.TenantID != tid {
		t.Fatalf("零行未填 tenant: %+v", row)
	}
	if row.QuotaPluginInstances != 0 || row.RetentionEventsDays != 0 || row.UpdatedAt != 0 {
		t.Fatalf("缺省行不是全 0（0=继承默认）: %+v", row)
	}
	got := row.Resolve()
	want := tenantpolicy.Defaults()
	if got != want {
		t.Fatalf("Resolve() = %+v, want defaults %+v", got, want)
	}
	if got.Quotas.PluginInstances != 100 || got.Retention.PluginObservations != 30 {
		t.Fatalf("关键默认值错误: %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("解析后的默认策略应合法: %v", err)
	}
}

// TestTenantPolicyRoundTripAndNullInherit 锁定：写入值原样读回；0 字段存 NULL 并继承默认，
// 非 0 字段覆盖默认（混合行只有被显式设置的字段生效）。
func TestTenantPolicyRoundTripAndNullInherit(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "pol-rt")

	in := TenantPolicyRow{
		RetentionEventsDays:         45,
		RetentionAuditDays:          120,
		RetentionPluginObservedDays: 7,
		QuotaPluginInstances:        25,
		QuotaDevices:                300,
		UpdatedAt:                   12345,
	}
	if err := s.SetTenantPolicy(tid, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTenantPolicy(tid)
	if err != nil {
		t.Fatal(err)
	}
	if got.RetentionEventsDays != 45 || got.RetentionAuditDays != 120 || got.RetentionPluginObservedDays != 7 {
		t.Fatalf("retention 往返失败: %+v", got)
	}
	if got.QuotaPluginInstances != 25 || got.QuotaDevices != 300 {
		t.Fatalf("quota 往返失败: %+v", got)
	}
	if got.UpdatedAt != 12345 {
		t.Fatalf("updated_at 往返失败: %d", got.UpdatedAt)
	}
	// 未设置字段仍是 0（NULL）→ Resolve 继承默认
	if got.RetentionCommandsDays != 0 || got.QuotaEdges != 0 || got.QuotaUsers != 0 {
		t.Fatalf("未设置字段应为 0/NULL: %+v", got)
	}
	res := got.Resolve()
	def := tenantpolicy.Defaults()
	if res.Retention.Events != 45 || res.Retention.Audit != 120 || res.Retention.PluginObservations != 7 {
		t.Fatalf("Resolve 覆盖失败: %+v", res.Retention)
	}
	if res.Retention.TerminalCommands != def.Retention.TerminalCommands ||
		res.Retention.RevokedTokens != def.Retention.RevokedTokens {
		t.Fatalf("Resolve 继承失败: %+v", res.Retention)
	}
	if res.Quotas.PluginInstances != 25 || res.Quotas.Devices != 300 {
		t.Fatalf("Resolve quota 覆盖失败: %+v", res.Quotas)
	}
	if res.Quotas.Edges != def.Quotas.Edges || res.Quotas.Users != def.Quotas.Users ||
		res.Quotas.EventsPerMinute != def.Quotas.EventsPerMinute {
		t.Fatalf("Resolve quota 继承失败: %+v", res.Quotas)
	}
	// NULL 确实落库为 NULL（不是 0），这是 §3.1「不能用 0 表示无限」的结构保证
	var nulls int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tenant_policies WHERE tenant_id=? AND
		(retention_commands_days IS NULL AND quota_edges IS NULL AND quota_users IS NULL)`,
		tid).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 1 {
		t.Fatalf("未设置字段没有存成 NULL: %d", nulls)
	}
}

// TestTenantPolicyRejectsInvalid 锁定：越界/负值被拒绝且**不写入任何列**（旧策略保持不变）。
// 范围来自 tenant-security-policy.md §3.1（audit 最少 7 天、上限 3650）与 §4/§5。
func TestTenantPolicyRejectsInvalid(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "pol-bad")
	base := TenantPolicyRow{QuotaPluginInstances: 25, RetentionEventsDays: 30}
	if err := s.SetTenantPolicy(tid, base); err != nil {
		t.Fatal(err)
	}

	bad := []struct {
		name string
		row  TenantPolicyRow
	}{
		{"audit 少于 7 天", TenantPolicyRow{RetentionAuditDays: 6}},
		{"retention 超上限", TenantPolicyRow{RetentionEventsDays: 3660}},
		{"retention 负值", TenantPolicyRow{RetentionEventsDays: -1}},
		{"quota 负值", TenantPolicyRow{QuotaPluginInstances: -5}},
		{"quota 超上限", TenantPolicyRow{QuotaDevices: 1000001}},
		{"events/min 超上限", TenantPolicyRow{QuotaEventsPerMinute: 2000000}},
	}
	for _, c := range bad {
		err := s.SetTenantPolicy(tid, c.row)
		if !errors.Is(err, tenantpolicy.ErrInvalidPolicy) {
			t.Fatalf("%s: err = %v, want ErrInvalidPolicy", c.name, err)
		}
		got, err := s.GetTenantPolicy(tid)
		if err != nil {
			t.Fatal(err)
		}
		if got.QuotaPluginInstances != 25 || got.RetentionEventsDays != 30 {
			t.Fatalf("%s: 非法写入改动了既有策略: %+v", c.name, got)
		}
	}
	// DB CHECK 是第二道防线：绕过 Go 层直接写非法值也必须被拒
	for _, q := range []string{
		`UPDATE tenant_policies SET retention_audit_days=6 WHERE tenant_id=?`,
		`UPDATE tenant_policies SET retention_events_days=0 WHERE tenant_id=?`,
		`UPDATE tenant_policies SET retention_events_days=3660 WHERE tenant_id=?`,
		`UPDATE tenant_policies SET quota_plugin_instances=0 WHERE tenant_id=?`,
		`UPDATE tenant_policies SET quota_plugin_instances=-5 WHERE tenant_id=?`,
		`UPDATE tenant_policies SET quota_devices=1000001 WHERE tenant_id=?`,
	} {
		if _, err := s.db.Exec(q, tid); err == nil {
			t.Fatalf("DB CHECK 未拦住非法值: %s", q)
		}
	}
	// 合法边界值必须通过
	for _, q := range []string{
		`UPDATE tenant_policies SET retention_audit_days=7 WHERE tenant_id=?`,
		`UPDATE tenant_policies SET retention_events_days=3650 WHERE tenant_id=?`,
		`UPDATE tenant_policies SET quota_plugin_instances=1000000 WHERE tenant_id=?`,
		`UPDATE tenant_policies SET quota_devices=NULL WHERE tenant_id=?`,
	} {
		if _, err := s.db.Exec(q, tid); err != nil {
			t.Fatalf("合法值被拒: %s: %v", q, err)
		}
	}
}

// TestTenantPolicyCannotMoveTenant 锁定契约「任何 upsert 不得修改既有行的 tenant_id」：
// row.TenantID 被忽略，归属恒取参数；策略绝不会跑到别的租户名下。
func TestTenantPolicyCannotMoveTenant(t *testing.T) {
	s := openTest(t)
	a := mkTenant(t, s, "pol-a")
	b := mkTenant(t, s, "pol-b")

	if err := s.SetTenantPolicy(a, TenantPolicyRow{TenantID: b, QuotaPluginInstances: 7}); err != nil {
		t.Fatal(err)
	}
	gotA, _ := s.GetTenantPolicy(a)
	if gotA.QuotaPluginInstances != 7 || gotA.TenantID != a {
		t.Fatalf("A 的策略未按参数 tenant 落库: %+v", gotA)
	}
	gotB, _ := s.GetTenantPolicy(b)
	if gotB.QuotaPluginInstances != 0 {
		t.Fatalf("策略被搬到 B: %+v", gotB)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tenant_policies`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("tenant_policies 行数 = %d, want 1（不得额外插入 B 的行）", n)
	}
	// 再次 Set 仍是同一行 upsert，不新增
	if err := s.SetTenantPolicy(a, TenantPolicyRow{QuotaPluginInstances: 9}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tenant_policies`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("重复 Set 新增了行: %d err=%v", n, err)
	}
	if gotA, _ = s.GetTenantPolicy(a); gotA.QuotaPluginInstances != 9 {
		t.Fatalf("upsert 未更新: %+v", gotA)
	}
}

// TestCountPluginInstances 锁定：计数口径 = Server 权威 desired（不数 Edge 上报投影），
// 且严格按租户隔离。
func TestCountPluginInstances(t *testing.T) {
	s := openTest(t)
	a := mkTenant(t, s, "cnt-a")
	b := mkTenant(t, s, "cnt-b")

	if n, err := s.CountPluginInstances(a); err != nil || n != 0 {
		t.Fatalf("空租户 count = %d err=%v", n, err)
	}
	for i := 1; i <= 3; i++ {
		mustCreate(t, s, desiredRow(a, "e1", fmt.Sprintf("i%d", i), "p1"))
	}
	mustCreate(t, s, desiredRow(b, "eB", "i1", "p1"))
	// Edge 上报投影不得影响 desired 计数
	if err := s.UpsertPluginObservations(a, "e1", []api.PluginObservedInstanceData{
		{InstanceID: "i1"}, {InstanceID: "i2"}, {InstanceID: "ghost"},
	}, 100); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountPluginInstances(a); err != nil || n != 3 {
		t.Fatalf("A count = %d err=%v, want 3", n, err)
	}
	if n, err := s.CountPluginInstances(b); err != nil || n != 1 {
		t.Fatalf("B count = %d err=%v, want 1", n, err)
	}
	if _, err := s.DeletePluginInstance(a, "e1", "i1", false); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountPluginInstances(a); n != 2 {
		t.Fatalf("删除后 count = %d, want 2", n)
	}
	if n, _ := s.CountPluginInstances(0); n != 0 {
		t.Fatalf("tenantID=0 count = %d, want 0（解析为 default 租户）", n)
	}
}

// TestPluginQuotaRejectsAtLimit 锁定暗卷：配额拒绝必须可与其他错误区分（稳定 sentinel），
// 且不得已经推进 revision、不得留下半状态行；Update 已有实例不占配额。
func TestPluginQuotaRejectsAtLimit(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "quota")
	if err := s.SetTenantPolicy(tid, TenantPolicyRow{QuotaPluginInstances: 2}); err != nil {
		t.Fatal(err)
	}

	mustCreate(t, s, desiredRow(tid, "e1", "i1", "p1"))
	mustCreate(t, s, desiredRow(tid, "e1", "i2", "p1"))
	revBefore, _ := s.PluginDesiredRevision(tid, "e1")
	if revBefore != 2 {
		t.Fatalf("revision = %d, want 2", revBefore)
	}

	_, err := s.CreatePluginInstance(desiredRow(tid, "e1", "i3", "p1"))
	if !errors.Is(err, ErrPluginQuotaExceeded) {
		t.Fatalf("err = %v, want ErrPluginQuotaExceeded", err)
	}
	// 可区分性：不得与 not-found / conflict / tenant-mismatch / 校验错误混淆
	for _, other := range []error{ErrPluginInstanceNotFound, ErrPluginInstanceConflict,
		ErrPluginIdentityIncomplete, ErrPluginConfigInvalid, ErrEdgeTenantMismatch, ErrPluginImmutableField} {
		if errors.Is(err, other) {
			t.Fatalf("配额错误与 %v 混淆", other)
		}
	}
	if !strings.Contains(err.Error(), string(tenantpolicy.ResourcePluginInstances)) {
		t.Fatalf("错误未携带稳定 resource 标识: %v", err)
	}
	// revision 未推进
	if rev, _ := s.PluginDesiredRevision(tid, "e1"); rev != revBefore {
		t.Fatalf("配额拒绝推进了 revision: %d -> %d", revBefore, rev)
	}
	// 无半状态行
	if _, ok, _ := s.GetPluginInstance(tid, "e1", "i3"); ok {
		t.Fatal("配额拒绝留下了半状态行")
	}
	if n, _ := s.CountPluginInstances(tid); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM plugin_desired_instances WHERE instance_id='i3'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("任何租户下都不该出现 i3: %d 行", rows)
	}
	// Update 既有实例不占配额，仍应成功并推进 revision
	up := desiredRow(tid, "e1", "i1", "p1")
	up.Version = "9.9.9"
	if rev, err := s.UpdatePluginInstance(up); err != nil || rev != 3 {
		t.Fatalf("update rev = %d err=%v, want 3", rev, err)
	}
	// 提高配额后立即可写（配额读取不是缓存的）
	if err := s.SetTenantPolicy(tid, TenantPolicyRow{QuotaPluginInstances: 3}); err != nil {
		t.Fatal(err)
	}
	if rev, err := s.CreatePluginInstance(desiredRow(tid, "e1", "i3", "p1")); err != nil || rev != 4 {
		t.Fatalf("提额后 create rev = %d err=%v, want 4", rev, err)
	}
}

// TestPluginQuotaConcurrentAtomic 锁定 tenant-security-policy.md §4.1：
// 计数与写入同事务，并发「刚好到上限」的请求只能有一个成功；
// 失败方全部拿到配额 sentinel，且 revision 恰好只前进 1（无空洞、无重复消费）。
func TestPluginQuotaConcurrentAtomic(t *testing.T) {
	s := openTest(t)
	tid := mkTenant(t, s, "quota-race")
	const limit = 5
	if err := s.SetTenantPolicy(tid, TenantPolicyRow{QuotaPluginInstances: limit}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < limit-1; i++ {
		mustCreate(t, s, desiredRow(tid, "e1", fmt.Sprintf("pre%d", i), "p1"))
	}
	revBefore, _ := s.PluginDesiredRevision(tid, "e1")

	const racers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	okCh := make(chan uint64, racers)
	quotaCh := make(chan struct{}, racers)
	otherCh := make(chan error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rev, err := s.CreatePluginInstance(desiredRow(tid, "e1", fmt.Sprintf("race%d", i), "p1"))
			switch {
			case err == nil:
				okCh <- rev
			case errors.Is(err, ErrPluginQuotaExceeded):
				quotaCh <- struct{}{}
			default:
				otherCh <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(okCh)
	close(quotaCh)
	close(otherCh)

	for err := range otherCh {
		t.Fatalf("并发写入出现非配额错误: %v", err)
	}
	won := len(okCh)
	rejected := len(quotaCh)
	if won != 1 {
		t.Fatalf("到上限时成功 %d 个, want 恰好 1（原子准入失效）", won)
	}
	if rejected != racers-1 {
		t.Fatalf("配额拒绝 %d 个, want %d", rejected, racers-1)
	}
	if n, _ := s.CountPluginInstances(tid); n != limit {
		t.Fatalf("count = %d, want %d（不得超配额）", n, limit)
	}
	revAfter, _ := s.PluginDesiredRevision(tid, "e1")
	if revAfter != revBefore+1 {
		t.Fatalf("revision %d -> %d, want 只前进 1（被拒写入不得消费 revision）", revBefore, revAfter)
	}
	for rev := range okCh {
		if rev != revAfter {
			t.Fatalf("成功方 revision = %d, want %d", rev, revAfter)
		}
	}
}

// TestPrunePluginObservationsByTenant 锁定 §3.1/§6.3：保留期清理恒带 tenant predicate，
// 清 A 不读/不删 B；只清 observed 投影，desired 与 audit 不动。
func TestPrunePluginObservationsByTenant(t *testing.T) {
	s := openTest(t)
	a := mkTenant(t, s, "prune-a")
	b := mkTenant(t, s, "prune-b")

	mustCreate(t, s, desiredRow(a, "e1", "i1", "p1"))
	if err := s.InsertAuditEvent(AuditEvent{TenantID: a, Action: "plugin.instance.created", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	// A：一条老投影（ts=100）一条新投影（ts=5000）；B：一条老投影
	if err := s.UpsertPluginObservations(a, "e1", []api.PluginObservedInstanceData{
		{InstanceID: "old", PluginID: "p1"},
	}, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPluginObservations(a, "e2", []api.PluginObservedInstanceData{
		{InstanceID: "new", PluginID: "p1"},
	}, 5000); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPluginObservations(b, "eB", []api.PluginObservedInstanceData{
		{InstanceID: "old", PluginID: "p1"},
	}, 100); err != nil {
		t.Fatal(err)
	}

	n, err := s.PrunePluginObservations(a, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1", n)
	}
	obsA, _ := s.ListPluginObservationsTenant(a)
	if len(obsA) != 1 || obsA[0].InstanceID != "new" {
		t.Fatalf("A 清理结果 = %+v, want 只剩 new", obsA)
	}
	obsB, _ := s.ListPluginObservationsTenant(b)
	if len(obsB) != 1 || obsB[0].InstanceID != "old" {
		t.Fatalf("清理 A 误删了 B: %+v", obsB)
	}
	// desired 与 audit 不随 observed 清理
	if cnt, _ := s.CountPluginInstances(a); cnt != 1 {
		t.Fatalf("desired 被误删: %d, want 1", cnt)
	}
	if audits, _ := s.ListAuditEvents(a, 0, "", 10); len(audits) != 1 {
		t.Fatalf("audit 被误删: %d, want 1", len(audits))
	}
	// 幂等 + 非法 before 拒绝
	if n, _ := s.PrunePluginObservations(a, 1000); n != 0 {
		t.Fatalf("二次清理 = %d, want 0（幂等）", n)
	}
	if _, err := s.PrunePluginObservations(a, 0); err == nil {
		t.Fatal("before=0 应被拒绝（否则会清空全部投影）")
	}
}

// TestNoPlaintextSecretColumns 是结构反向验证：v7/v8 任何表都不得出现 secret 明文列。
// 唯一含 "secret" 的列必须是 plugin_desired_instances.secret_refs（只存 handle 名称）。
// 若将来有人给这些表加 password/plaintext/env 之类列，本测试必须失败。
func TestNoPlaintextSecretColumns(t *testing.T) {
	s := openTest(t)
	tables := []string{
		"plugin_desired_instances", "plugin_edge_revisions",
		"plugin_installations", "plugin_observations", "tenant_policies",
	}
	forbidden := []string{"password", "plaintext", "secret_value", "token_value", "env", "argv", "path", "stdout", "stderr"}
	for _, table := range tables {
		rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for rows.Next() {
			var (
				cid, notNull, pk int
				name, typ        string
				dflt             sql.NullString
			)
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			cols = append(cols, name)
			lower := strings.ToLower(name)
			for _, bad := range forbidden {
				if strings.Contains(lower, bad) {
					rows.Close()
					t.Fatalf("表 %s 含禁用列 %q（secret/路径/进程明文不得入库）", table, name)
				}
			}
			if strings.Contains(lower, "secret") && !(table == "plugin_desired_instances" && lower == "secret_refs") {
				rows.Close()
				t.Fatalf("表 %s 含意外 secret 列 %q", table, name)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if len(cols) == 0 {
			t.Fatalf("表 %s 不存在", table)
		}
	}
}
