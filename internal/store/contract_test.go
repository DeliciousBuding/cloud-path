package store

import (
	"reflect"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// TestPluginStoreContractSignatures 在**编译期**锁定 .local/plan/v0.1-completion.md §3 契约。
// 每个导出方法都被赋给一个精确签名的函数变量：方法名、参数个数/类型、返回值任何一处漂移都会
// 让本文件编译失败，而不是等到 Captain 把真实 store 接到 Server lane 时才暴露。
// 因此本测试的断言主体就是这些赋值；运行时只额外校验 nil receiver 不被内部处理（契约要求
// 调用方判空，store 内部不做 nil 兜底）。
func TestPluginStoreContractSignatures(t *testing.T) {
	var s *Store // 只取方法值绑定，从不调用，因此不会解引用 nil
	var (
		_ func(int64) ([]PluginInstanceRow, error)                           = s.ListPluginInstancesTenant
		_ func(int64, string, string) (PluginInstanceRow, bool, error)       = s.GetPluginInstance
		_ func(PluginInstanceRow) (uint64, error)                            = s.CreatePluginInstance
		_ func(PluginInstanceRow) (uint64, error)                            = s.UpdatePluginInstance
		_ func(int64, string, string, bool) (uint64, error)                  = s.DeletePluginInstance
		_ func(int64, string) (uint64, error)                                = s.PluginDesiredRevision
		_ func(int64, string) (PluginEdgeRevisionRow, error)                 = s.GetPluginEdgeRevision
		_ func(int64, string, string, uint64, uint64, int64) error           = s.SetPluginEdgeApplied
		_ func(int64, string, string, uint64, int64) error                   = s.SetPluginEdgeReport
		_ func(int64, string, []api.PluginInstallationStatusData) error      = s.UpsertPluginInstallations
		_ func(int64, string, []api.PluginObservedInstanceData, int64) error = s.UpsertPluginObservations
		_ func(int64) ([]PluginObservationRow, error)                        = s.ListPluginObservationsTenant
		_ func(int64) ([]PluginInstallationRow, error)                       = s.ListPluginInstallationsTenant
		_ func(int64) (TenantPolicyRow, error)                               = s.GetTenantPolicy
		_ func(int64, TenantPolicyRow) error                                 = s.SetTenantPolicy
		_ func(int64) (int, error)                                           = s.CountPluginInstances
	)
	if s != nil {
		t.Fatal("测试前提被破坏：s 必须是 nil *Store")
	}
}

// contractFields 逐字段锁定 §3 明确定义了字段集的两个结构体（名称+类型+顺序）。
// 字段改名/改类型会让 Server lane 静默读到零值，因此在 store 侧硬断言。
func TestPluginStoreContractStructFields(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		want []string
	}{
		{
			name: "PluginInstanceRow",
			typ:  reflect.TypeOf(PluginInstanceRow{}),
			want: []string{
				"TenantID int64", "EdgeID string", "InstanceID string", "PluginID string",
				"Version string", "Enabled bool", "Isolation string", "ConfigJSON string",
				"SecretRefs string", "Revision uint64", "CreatedAt int64", "UpdatedAt int64",
			},
		},
		{
			name: "PluginEdgeRevisionRow",
			typ:  reflect.TypeOf(PluginEdgeRevisionRow{}),
			want: []string{
				"TenantID int64", "EdgeID string", "DesiredRevision uint64", "AppliedRevision uint64",
				"BootID string", "LastSequence uint64", "LastReportAt int64", "LastAckAt int64",
			},
		},
	}
	for _, c := range cases {
		if c.typ.NumField() != len(c.want) {
			t.Fatalf("%s 字段数 = %d, want %d", c.name, c.typ.NumField(), len(c.want))
		}
		for i, w := range c.want {
			f := c.typ.Field(i)
			got := f.Name + " " + f.Type.String()
			if got != w {
				t.Fatalf("%s 第 %d 字段 = %q, want %q", c.name, i, got, w)
			}
		}
	}
}

// TestProjectionRowsCarryFrozenDTO 锁定投影行内嵌冻结 DTO：Server lane 可直接把
// PluginObservationRow / PluginInstallationRow 的内嵌字段当作 api DTO 使用，
// 不需要 store 再抄一份字段（避免两处定义漂移）。
func TestProjectionRowsCarryFrozenDTO(t *testing.T) {
	obs := reflect.TypeOf(PluginObservationRow{})
	inst := reflect.TypeOf(PluginInstallationRow{})
	if f, ok := obs.FieldByName("PluginObservedInstanceData"); !ok || !f.Anonymous {
		t.Fatal("PluginObservationRow 必须内嵌 api.PluginObservedInstanceData")
	}
	if f, ok := inst.FieldByName("PluginInstallationStatusData"); !ok || !f.Anonymous {
		t.Fatal("PluginInstallationRow 必须内嵌 api.PluginInstallationStatusData")
	}
	// 归属坐标必须可直接访问（Server 组装 PluginInstanceView 需要 tenant/edge/reported_at）
	for _, typ := range []reflect.Type{obs, inst} {
		for _, name := range []string{"TenantID", "EdgeID", "ReportedAt"} {
			if _, ok := typ.FieldByName(name); !ok {
				t.Fatalf("%s 缺字段 %s", typ.Name(), name)
			}
		}
	}
}
