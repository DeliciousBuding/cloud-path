package store

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestAuditAppendOnly 锁定红线：Store 不暴露任何 audit_events 的 update/delete API，
// 事件只追加、id 单调递增、按 id 倒序可查询。
func TestAuditAppendOnly(t *testing.T) {
	typ := reflect.TypeOf(&Store{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !strings.Contains(strings.ToLower(name), "audit") {
			continue
		}
		if strings.Contains(name, "Update") || strings.Contains(name, "Delete") {
			t.Fatalf("audit_events 必须 append-only，发现变更方法 %s", name)
		}
	}

	st, err := Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tenant, err := st.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := st.InsertAuditEvent(AuditEvent{
			TenantID: tenant, ActorType: "user", ActorID: 1, ActorName: "admin",
			Action: "auth.login", TargetType: "tenant", Outcome: "success",
			MetadataJSON: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.ListAuditEvents(tenant, 0, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("events = %d, want 3", len(rows))
	}
	if rows[0].ID <= rows[1].ID || rows[1].ID <= rows[2].ID {
		t.Fatalf("应倒序且 id 单调递增: %+v", rows)
	}
	for _, r := range rows {
		if r.Action != "auth.login" || r.ActorName != "admin" {
			t.Fatalf("事件字段未持久化: %+v", r)
		}
	}
}

// TestAuditTenantIsolation 锁定跨租户不可见：查询恒按 tenant_id 过滤。
func TestAuditTenantIsolation(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "audit-tenant.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a, err := st.CreateTenant("tenant-a", "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateTenant("tenant-b", "B")
	if err != nil {
		t.Fatal(err)
	}
	insert := func(tenant int64, action string) {
		t.Helper()
		if err := st.InsertAuditEvent(AuditEvent{
			TenantID: tenant, ActorType: "system", Action: action, MetadataJSON: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert(a, "auth.login")
	insert(a, "user.create")
	insert(b, "token.create")

	ra, err := st.ListAuditEvents(a, 0, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := st.ListAuditEvents(b, 0, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ra) != 2 || len(rb) != 1 {
		t.Fatalf("隔离失败: a=%d b=%d", len(ra), len(rb))
	}
	for _, r := range ra {
		if r.TenantID != a {
			t.Fatalf("tenant-a 查询泄漏: %+v", r)
		}
	}
	if rb[0].Action != "token.create" || rb[0].TenantID != b {
		t.Fatalf("tenant-b 查询异常: %+v", rb)
	}
}
