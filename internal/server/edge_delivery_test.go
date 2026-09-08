package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

type failingEdgeWriter struct {
	err error
}

func (w failingEdgeWriter) Write(context.Context, websocket.MessageType, []byte) error {
	return w.err
}

// TestEdgeWriteFailureUnblocksCommand 锁定半死连接的写失败路径：writer 失败后
// link.done 必须关闭，等待投递结果的命令立即拿到错误，不能静默留在旧缓冲区。
func TestEdgeWriteFailureUnblocksCommand(t *testing.T) {
	link := &edgeLink{
		edgeID: "e1", commandSend: make(chan edgeCommandFrame, 1),
		done: make(chan struct{}), cancel: func() {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writeErr := errors.New("broken websocket")
	go writeEdgePump(ctx, cancel, failingEdgeWriter{err: writeErr}, link)

	if err := link.sendCommand(context.Background(), []byte(`{"type":"command"}`)); err == nil {
		t.Fatal("writer 失败时 sendCommand 必须返回错误")
	}
	select {
	case <-link.done:
	case <-time.After(time.Second):
		t.Fatal("writer 失败后 link.done 未关闭")
	}
}

// TestDispatchDeviceCommandClosedLinkFails 锁定旧/半死链路：命令不得只进旧
// link 的缓冲区后标记 sent；必须落 failed 并把错误返回给调用方。
func TestDispatchDeviceCommandClosedLinkFails(t *testing.T) {
	srv, _ := setup(t)
	tid := ensureTenantSlug(t, srv.cfg.Store, "edge-delivery")
	key := "e1/d1"
	if err := srv.cfg.Store.UpsertDeviceTenant(key, "e1", "demo", key, "", tid); err != nil {
		t.Fatal(err)
	}
	link := &edgeLink{
		edgeID: "e1", tenant: "edge-delivery", tenantID: tid,
		commandSend: make(chan edgeCommandFrame, 1), done: make(chan struct{}), cancel: func() {},
	}
	close(link.done)
	srv.mu.Lock()
	srv.edges["e1"] = link
	srv.devices[key] = onlineDevice(key, "e1")
	srv.mu.Unlock()

	id, err := srv.dispatchDeviceCommand(context.Background(), tid, key, "buzzer", `{"freq":1}`)
	if err == nil {
		t.Fatal("旧 link 关闭时命令必须失败")
	}
	if id == 0 {
		t.Fatal("命令已落库时必须返回 command id，便于调用方通知终态")
	}
	rows, err := srv.cfg.Store.ListCommandsTenant(tid, key, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id || rows[0].Status != "failed" {
		t.Fatalf("旧 link 命令状态 = %+v, want id=%d status=failed", rows, id)
	}
}

// TestDispatchDeviceCommandUsesReconnectedLink 锁定重连后的当前链路选择：
// 旧 link 被替换后，新命令只投递到新 link，并真实标 sent。
func TestDispatchDeviceCommandUsesReconnectedLink(t *testing.T) {
	srv, _ := setup(t)
	tid := ensureTenantSlug(t, srv.cfg.Store, "edge-reconnect")
	key := "e1/d1"
	if err := srv.cfg.Store.UpsertDeviceTenant(key, "e1", "demo", key, "", tid); err != nil {
		t.Fatal(err)
	}

	oldLink := &edgeLink{
		edgeID: "e1", tenant: "edge-reconnect", tenantID: tid,
		commandSend: make(chan edgeCommandFrame, 1), done: make(chan struct{}), cancel: func() {},
	}
	close(oldLink.done)
	newLink := &edgeLink{
		edgeID: "e1", tenant: "edge-reconnect", tenantID: tid,
		commandSend: make(chan edgeCommandFrame, 1), done: make(chan struct{}), cancel: func() {},
	}
	srv.mu.Lock()
	srv.edges["e1"] = oldLink
	srv.devices[key] = onlineDevice(key, "e1")
	srv.mu.Unlock()
	srv.mu.Lock()
	srv.edges["e1"] = newLink // 模拟同租户重连挤掉旧 link
	srv.mu.Unlock()

	got := make(chan edgeCommandFrame, 1)
	go func() {
		frame := <-newLink.commandSend
		got <- frame
		frame.result <- nil
	}()
	id, err := srv.dispatchDeviceCommand(context.Background(), tid, key, "buzzer", `{"freq":2}`)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case frame := <-got:
		var env struct {
			Type api.MsgType `json:"type"`
		}
		if err := json.Unmarshal(frame.payload, &env); err != nil || env.Type != api.MsgCommand {
			t.Fatalf("新 link 命令信封 = %s err=%v", frame.payload, err)
		}
	case <-time.After(time.Second):
		t.Fatal("重连后的新 link 未收到命令")
	}
	select {
	case <-oldLink.commandSend:
		t.Fatal("命令误投到已失效的旧 link")
	default:
	}
	rows, err := srv.cfg.Store.ListCommandsTenant(tid, key, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id || rows[0].Status != "sent" {
		t.Fatalf("重连命令状态 = %+v, want id=%d status=sent", rows, id)
	}
}

// TestDispatchDeviceCommandHookBeforeWrite 锁定 AppHost 的 ACK 登记时序：
// onCreated 必须在任何 Edge 写入前完成，否则极快 ACK 会先到而引用尚未登记。
func TestDispatchDeviceCommandHookBeforeWrite(t *testing.T) {
	srv, _ := setup(t)
	tid := ensureTenantSlug(t, srv.cfg.Store, "edge-hook-order")
	key := "e1/d1"
	if err := srv.cfg.Store.UpsertDeviceTenant(key, "e1", "demo", key, "", tid); err != nil {
		t.Fatal(err)
	}
	link := &edgeLink{
		edgeID: "e1", tenant: "edge-hook-order", tenantID: tid,
		commandSend: make(chan edgeCommandFrame, 1), done: make(chan struct{}), cancel: func() {},
	}
	srv.mu.Lock()
	srv.edges["e1"] = link
	srv.devices[key] = onlineDevice(key, "e1")
	srv.mu.Unlock()

	hookSeen := make(chan int64, 1)
	writeResult := make(chan error, 1)
	go func() {
		frame := <-link.commandSend
		select {
		case id := <-hookSeen:
			if id == 0 {
				writeResult <- errors.New("hook received zero command id")
				frame.result <- errors.New("hook received zero command id")
				return
			}
			writeResult <- nil
			frame.result <- nil
		default:
			err := errors.New("command reached Edge before onCreated hook")
			writeResult <- err
			frame.result <- err
		}
	}()

	id, err := srv.dispatchDeviceCommandWithHook(context.Background(), tid, key, "buzzer", `{"freq":1}`, func(id int64) { hookSeen <- id })
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("command id must be non-zero")
	}
	if err := <-writeResult; err != nil {
		t.Fatal(err)
	}
}

// TestTimeoutOnceNotifiesAppCommand 锁定 90s sweeper 的终态通知：只有真正
// pending/sent → timeout 的命令才消费 AppHost 引用并触发 RequestCompleted。
func TestTimeoutOnceNotifiesAppCommand(t *testing.T) {
	srv, _ := setup(t)
	tid := ensureTenantSlug(t, srv.cfg.Store, "edge-timeout")
	key := "e1/d1"
	if err := srv.cfg.Store.UpsertDeviceTenant(key, "e1", "demo", key, "", tid); err != nil {
		t.Fatal(err)
	}
	id, err := srv.cfg.Store.CreateCommandTenant(key, "buzzer", "{}", tid)
	if err != nil {
		t.Fatal(err)
	}

	ah, err := NewAppHost(srv, AppHostConfig{
		Enabled: true, PluginsDir: t.TempDir(),
		LockPath: filepath.Join(t.TempDir(), "plugins.lock"), StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ah.Close)
	srv.SetAppHost(ah)
	ah.mu.Lock()
	ah.appCmds[id] = appCommandRef{
		TenantID: strconv.FormatInt(tid, 10), InstanceID: "app-timeout",
		RequestID: "req-timeout", EntityID: "buzzer", Action: "buzzer",
	}
	ah.mu.Unlock()

	// 负 TTL 让 cutoff 落在未来，避免秒级时间边界 sleep。
	n, err := srv.timeoutOnce(-120 * time.Second)
	if err != nil || n != 1 {
		t.Fatalf("timeoutOnce = %d err=%v, want 1", n, err)
	}
	ah.mu.Lock()
	_, still := ah.appCmds[id]
	ah.mu.Unlock()
	if still {
		t.Fatal("timeout 命令的 AppHost 引用未消费，应用收不到 RequestCompleted")
	}
	rows, err := srv.cfg.Store.ListCommandsTenant(tid, key, "timeout", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("timeout 命令未落库: rows=%+v err=%v", rows, err)
	}
}
