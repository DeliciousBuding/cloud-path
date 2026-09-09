package edge

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/device"
)

type whitelistTestAdapter struct {
	name string
	cmds []string
	dev  *whitelistTestDevice
}

func (a *whitelistTestAdapter) Name() string { return a.name }

func (a *whitelistTestAdapter) SupportedCommands() []string {
	return append([]string(nil), a.cmds...)
}

func (a *whitelistTestAdapter) Open(context.Context, device.Config, func(device.Event)) (device.Device, error) {
	return a.dev, nil
}

type whitelistTestDevice struct {
	sends atomic.Int64
	done  chan struct{}
}

func newWhitelistTestDevice() *whitelistTestDevice {
	return &whitelistTestDevice{done: make(chan struct{})}
}

func (d *whitelistTestDevice) ID() string { return "d1" }

func (d *whitelistTestDevice) Snapshot() device.State {
	return device.State{Online: true, Raw: map[string]any{}, UpdatedAt: time.Now()}
}

func (d *whitelistTestDevice) Send(context.Context, device.Command) error {
	d.sends.Add(1)
	return nil
}

func (d *whitelistTestDevice) Done() <-chan struct{} { return d.done }
func (d *whitelistTestDevice) Close() error          { return nil }

type whitelistAck struct {
	api.AckData
	Device string
}

func newWhitelistTestEdge(key string, sup *supervisor) *Edge {
	return &Edge{
		cfg:  &Config{ReportIntervalS: 30},
		ctx:  context.Background(),
		sups: map[string]*supervisor{key: sup},
		client: &wsClient{
			online: true,
			send:   make(chan []byte, 8),
		},
	}
}

func runWhitelistCommand(t *testing.T, e *Edge, key string, commandID int64, cmd string) whitelistAck {
	t.Helper()
	e.onCommand(api.Envelope{
		V:      api.Version,
		Type:   api.MsgCommand,
		Device: key,
		Ts:     time.Now().Unix(),
		Data: mustJSON(api.CommandData{
			CommandID: commandID,
			Cmd:       cmd,
		}),
	})

	select {
	case raw := <-e.client.send:
		var env api.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("unmarshal command_ack envelope: %v", err)
		}
		if env.Type != api.MsgCommandAck {
			t.Fatalf("envelope type = %q, want %q", env.Type, api.MsgCommandAck)
		}
		var ack api.AckData
		if err := json.Unmarshal(env.Data, &ack); err != nil {
			t.Fatalf("unmarshal command_ack: %v", err)
		}
		return whitelistAck{AckData: ack, Device: env.Device}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 command_ack")
	}
	return whitelistAck{}
}

func TestEdgeCommandWhitelistAllowsSupportedCommand(t *testing.T) {
	dev := newWhitelistTestDevice()
	adapter := &whitelistTestAdapter{name: "whitelist-ok", cmds: []string{"ping"}, dev: dev}
	key := "e1/d1"
	e := newWhitelistTestEdge(key, &supervisor{
		edgeID:  "e1",
		dcfg:    DeviceCfg{ID: "d1"},
		adapter: adapter,
		dev:     dev,
	})

	ack := runWhitelistCommand(t, e, key, 101, "ping")
	if ack.Status != "ok" || ack.CommandID != 101 || ack.Device != key {
		t.Fatalf("支持命令的 ack = %+v", ack)
	}
	if strings.TrimSpace(ack.Detail) == "" {
		t.Fatal("成功 ack 必须带执行结果摘要")
	}
	if got := dev.sends.Load(); got != 1 {
		t.Fatalf("支持命令到达 Driver 的次数 = %d, want 1", got)
	}
}

func TestEdgeCommandWhitelistRejectsUnsupportedBeforeDeviceSend(t *testing.T) {
	dev := newWhitelistTestDevice()
	adapter := &whitelistTestAdapter{name: "whitelist-reject", cmds: []string{"ping"}, dev: dev}
	key := "e1/d1"
	e := newWhitelistTestEdge(key, &supervisor{
		edgeID:  "e1",
		dcfg:    DeviceCfg{ID: "d1"},
		adapter: adapter,
		dev:     dev,
	})

	ack := runWhitelistCommand(t, e, key, 102, "sync")
	if ack.Status != "failed" || ack.CommandID != 102 || ack.Device != key {
		t.Fatalf("白名单外命令的 ack = %+v", ack)
	}
	wantDetail := `unsupported_command: 不支持的命令 "sync"`
	if ack.Detail != wantDetail {
		t.Fatalf("白名单外命令 detail = %q, want %q", ack.Detail, wantDetail)
	}
	if got := dev.sends.Load(); got != 0 {
		t.Fatalf("白名单外命令触发 Driver Send 次数 = %d, want 0", got)
	}
}

func TestExternalDriverCommandWhitelistRejectsUnsupportedBeforeExecute(t *testing.T) {
	f := openFleet(t)
	node := f.nodes[0]
	key := api.DeviceKey("e1", node.id)
	e := newWhitelistTestEdge(key, &supervisor{
		edgeID:  "e1",
		dcfg:    DeviceCfg{ID: node.id},
		adapter: f.ad,
		dev:     f.devs[node.id],
	})

	unsupported := runWhitelistCommand(t, e, key, 201, "not-declared")
	if unsupported.Status != "failed" {
		t.Fatalf("外部 Driver 白名单外命令应回 failed，got %+v", unsupported)
	}
	if !strings.Contains(unsupported.Detail, "unsupported_command:") {
		t.Fatalf("外部 Driver 白名单外命令 detail 缺少稳定错误码: %q", unsupported.Detail)
	}
	if execs := f.rec.executesSnapshot(); len(execs) != 0 {
		t.Fatalf("白名单外命令不得触发外部 Driver Execute: %+v", execs)
	}

	supported := runWhitelistCommand(t, e, key, 202, "switch")
	if supported.Status != "ok" {
		t.Fatalf("外部 Driver 支持命令应回 ok，got %+v", supported)
	}
	execs := f.rec.executesSnapshot()
	if len(execs) != 1 {
		t.Fatalf("支持命令应触发一次外部 Driver Execute，got %+v", execs)
	}
	if execs[0].DeviceID != node.id || execs[0].Action != "switch" || execs[0].IdempotencyKey != "202-switch" {
		t.Fatalf("外部 Driver Execute 路由/幂等键错误: %+v", execs[0])
	}
}
