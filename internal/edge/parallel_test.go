package edge

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/device"
)

// TestSameDeviceSerializedWhileDevicesParallel 锁定多设备硬化的两个方向：
//   - 跨设备**并行**：d1 的慢命令不得阻塞 d2 的命令（验收要求「分别发送控制命令」）；
//   - 同设备**串行**：同一台设备的第二条命令必须排队到第一条完成，否则两条命令
//     会在同一条物理线上互相插字节（stcb 的慢发对时命令缓冲只有 1 字节，
//     并发写会同时写坏两条命令）。
func TestSameDeviceSerializedWhileDevicesParallel(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	a1 := &gatedAdapter{name: fmt.Sprintf("gated-a-%d", gatedSeq.Add(1)), gate: make(chan struct{}), entered: make(chan string, 4)}
	a2 := &gatedAdapter{name: fmt.Sprintf("gated-b-%d", gatedSeq.Add(1)), gate: make(chan struct{}), entered: make(chan string, 4)}
	device.Register(a1)
	device.Register(a2)

	cfg := &Config{
		Server: rec.url("ws"), EdgeID: "e-par",
		PollIntervalS: 3600, SyncIntervalS: 3600, ReportIntervalS: 30,
		Devices: []DeviceCfg{
			{ID: "d1", Adapter: a1.name, Port: "P1", Baud: 9600},
			{ID: "d2", Adapter: a2.name, Port: "P2", Baud: 9600},
		},
	}
	runEdge(t, cfg)
	k1, k2 := api.DeviceKey("e-par", "d1"), api.DeviceKey("e-par", "d2")
	rec.waitState(t, k1, func(st api.StateData) bool { return st.Online })
	rec.waitState(t, k2, func(st api.StateData) bool { return st.Online })

	// 两台设备各下一条慢命令：必须**同时**进入执行（跨设备并行）。
	rec.sendCommand(t, k1, 1001, "trigger", "")
	rec.sendCommand(t, k2, 1002, "trigger", "")
	waitEntered(t, a1, "d1 的第一条命令未进入设备")
	waitEntered(t, a2, "d2 的命令被 d1 的慢命令阻塞（跨设备必须并行）")

	// 同一台设备再下一条：必须排队，不得在第一条完成前进入设备。
	rec.sendCommand(t, k1, 1003, "trigger", "")
	select {
	case got := <-a1.entered:
		t.Fatalf("同一台设备的命令必须串行，第二条却在第一条完成前进入了设备: %q", got)
	case <-time.After(400 * time.Millisecond):
	}

	// 放行 d1：第一条完成后第二条才允许进入。
	close(a1.gate)
	waitEntered(t, a1, "放行后 d1 的第二条命令未进入设备（串行队列卡死）")
	close(a2.gate)

	for _, tc := range []struct {
		id  int64
		key string
	}{{1001, k1}, {1002, k2}, {1003, k1}} {
		ack := rec.waitAck(t, tc.id)
		if ack.Status != "ok" || ack.Device != tc.key {
			t.Fatalf("ack %d = %+v, want ok/%s", tc.id, ack, tc.key)
		}
		if ack.Detail == "" {
			t.Fatalf("ack %d 的 detail 为空", tc.id)
		}
	}
}

func waitEntered(t *testing.T, a *gatedAdapter, why string) {
	t.Helper()
	select {
	case <-a.entered:
	case <-time.After(30 * time.Second):
		t.Fatal(why)
	}
}
