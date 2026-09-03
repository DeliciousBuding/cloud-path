package device_test

import (
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/device"
)

// TestRegistryStartsEmpty 是「Core 不导入任何具体 Driver 也可编译/运行」的证明：
// 本测试进程只 import Core 的 device 抽象（薄兼容层 → driverkit），没有空白导入
// examples/stcb，因此注册表必须为空；edge/server 的上层运行时依赖的正是这个空表回落路径。
func TestRegistryStartsEmpty(t *testing.T) {
	if got := device.Names(); len(got) != 0 {
		t.Fatalf("registry should start empty, got %v", got)
	}
	if _, ok := device.Get("stcb"); ok {
		t.Fatal("stcb must not be registered in a Core-only test process")
	}
}
