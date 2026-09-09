package store

import (
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// TestPluginStoreContractSignatures 在编译期锁定 Store 的持久化契约。
// 每个导出方法都被赋给一个精确签名的函数变量：方法名、参数个数/类型、返回值任何一处漂移都会
// 让本文件编译失败。运行时只额外校验 nil receiver 不被内部处理（调用方必须判空，
// Store 内部不做 nil 兜底）。
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
