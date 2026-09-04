package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/device"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
)

// 本文件验证 Core 侧的多设备不变量：同一份外部 Driver 贡献（一个 driver ID）
// 必须能独立打开、观测、命令并管理 >=3 台设备，且每台设备的身份、连接提示、
// 逻辑实例、Watch 流、命令路由与生命周期彼此隔离；一台设备关闭或断流不得
// 影响兄弟设备。测试全部使用 generic 命名（fleet/node/probe/relay），
// 不出现任何具体设备或厂商语义——桥接层认识的是 Driver Protocol v1，不是硬件。

const (
	fleetDriverID        = "io.test/fleet-driver"
	fleetOtherDriverID   = "vendor.example/other-thing"
	fleetCapabilityProbe = "cloudpath.test/capability/probe@1"
	fleetCapabilityRelay = "cloudpath.test/capability/relay@1"
	fleetEntityProbe     = "probe"
	fleetEntityRelay     = "relay"
)

// fleetNode 是一台待接入设备的本地物理绑定（等价于 edge.yaml 里的一个 device 条目）。
type fleetNode struct {
	id   string
	name string
	port string
	baud int
	role string
}

func fleetNodes() []fleetNode {
	return []fleetNode{
		{id: "node-a", name: "Node A", port: "tty-node-a", baud: 9600, role: "alpha"},
		{id: "node-b", name: "Node B", port: "tty-node-b", baud: 115200, role: "beta"},
		{id: "node-c", name: "Node C", port: "tty-node-c", baud: 57600, role: "gamma"},
	}
}

func (n fleetNode) config() device.Config {
	return device.Config{
		ID:    n.id,
		Name:  n.name,
		Port:  n.port,
		Baud:  n.baud,
		Extra: map[string]string{"role": n.role},
	}
}

func (n fleetNode) instanceID() string { return externalDeviceInstanceID(fleetDriverID, n.id) }

// ---- Driver Protocol v1 客户端替身 ----

type fleetOpenCall struct {
	InstanceID string
	DeviceID   string
	Hints      map[string]string
}

type fleetCloseCall struct {
	InstanceID string
	DeviceID   string
}

type fleetWatchCall struct {
	InstanceID string
	DeviceIDs  []string
}

type fleetExecuteCall struct {
	InstanceID     string
	DeviceID       string
	Action         string
	ArgsJSON       string
	IdempotencyKey string
	CommandID      string
}

// fleetRecorder 按「一台设备一个逻辑实例」记录全部协议调用，并可对指定实例注入
// 失败，用于验证桥接层不会把某台设备的错误/断流扩散到兄弟设备。
type fleetRecorder struct {
	mu sync.Mutex

	initializeCalls int
	describeCalls   int
	commandSeq      int

	configs map[string]string
	opens   []fleetOpenCall
	closes  []fleetCloseCall
	watches []fleetWatchCall
	execs   []fleetExecuteCall
	streams map[string]*fleetWatchStream

	openErrOn      map[string]error
	openStatusOn   map[string]*driver.Status
	executeErrOn   map[string]error
	executeStateOn map[string]driver.CommandState
}

func newFleetRecorder() *fleetRecorder {
	return &fleetRecorder{
		configs:        map[string]string{},
		streams:        map[string]*fleetWatchStream{},
		openErrOn:      map[string]error{},
		openStatusOn:   map[string]*driver.Status{},
		executeErrOn:   map[string]error{},
		executeStateOn: map[string]driver.CommandState{},
	}
}

func (r *fleetRecorder) Initialize(_ context.Context, req *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	r.mu.Lock()
	r.initializeCalls++
	r.mu.Unlock()
	if req.ProtocolVersion != driver.ProtocolVersion {
		return nil, fmt.Errorf("initialize: protocol version = %d, want %d", req.ProtocolVersion, driver.ProtocolVersion)
	}
	return &driver.InitializeResponse{
		NegotiatedProtocolVersion: driver.ProtocolVersion,
		Status:                    status.New(),
		RuntimeID:                 "fleet-recorder",
	}, nil
}

// Describe 返回 generic 能力目录：命令白名单必须来自插件自述，而不是 Core 内置清单。
func (r *fleetRecorder) Describe(_ context.Context) (*driver.DriverDescriptor, error) {
	r.mu.Lock()
	r.describeCalls++
	r.mu.Unlock()
	return &driver.DriverDescriptor{
		DriverID:       "fleet-recorder",
		Version:        "1.0.0",
		SchemaVersions: []string{driver.SchemaVersion},
		Capabilities: []driver.CapabilityDescriptor{
			{
				ID:    fleetCapabilityProbe,
				Title: "Probe",
				Properties: []driver.PropertyDescriptor{
					{Name: "value", Type: "number", Unit: "celsius", Access: "read"},
				},
				Actions: []driver.ActionDescriptor{{Name: "sample"}},
			},
			{
				ID:    fleetCapabilityRelay,
				Title: "Relay",
				Properties: []driver.PropertyDescriptor{
					{Name: "state", Type: "bool", Access: "readwrite"},
				},
				Actions: []driver.ActionDescriptor{{Name: "switch"}},
			},
		},
	}, nil
}

func (r *fleetRecorder) ConfigureInstance(_ context.Context, req *driver.ConfigureInstanceRequest) (*driver.ConfigureInstanceResponse, error) {
	if req.PluginInstanceID == "" {
		return nil, errors.New("configure instance: empty plugin_instance_id")
	}
	var cfg map[string]any
	if err := json.Unmarshal(req.Config, &cfg); err != nil {
		return nil, fmt.Errorf("configure instance: bad config json: %w", err)
	}
	if devID, _ := cfg["device_id"].(string); devID == "" {
		return nil, errors.New("configure instance: config 缺少 device_id")
	}
	r.mu.Lock()
	r.configs[req.PluginInstanceID] = string(req.Config)
	r.mu.Unlock()
	return &driver.ConfigureInstanceResponse{
		PluginInstanceID: req.PluginInstanceID,
		AppliedRevision:  req.ConfigRevision,
		Status:           status.New(),
	}, nil
}

func (r *fleetRecorder) Discover(_ context.Context, _ *driver.DiscoverRequest) (driver.DiscoveryStream, error) {
	return nil, errors.New("fleetRecorder: discovery 未在本测试使用")
}

func (r *fleetRecorder) OpenDevice(_ context.Context, req *driver.OpenDeviceRequest) (*driver.OpenDeviceResponse, error) {
	r.mu.Lock()
	if err := r.openErrOn[req.PluginInstanceID]; err != nil {
		r.mu.Unlock()
		return nil, err
	}
	st := r.openStatusOn[req.PluginInstanceID]
	hints := map[string]string{}
	for k, v := range req.ConnectionHints {
		hints[k] = v
	}
	r.opens = append(r.opens, fleetOpenCall{InstanceID: req.PluginInstanceID, DeviceID: req.DeviceID, Hints: hints})
	r.mu.Unlock()
	if st != nil {
		return &driver.OpenDeviceResponse{PluginInstanceID: req.PluginInstanceID, DeviceID: req.DeviceID, Status: st}, nil
	}
	return &driver.OpenDeviceResponse{PluginInstanceID: req.PluginInstanceID, DeviceID: req.DeviceID, Status: status.New()}, nil
}

func (r *fleetRecorder) CloseDevice(_ context.Context, req *driver.CloseDeviceRequest) (*driver.CloseDeviceResponse, error) {
	r.mu.Lock()
	r.closes = append(r.closes, fleetCloseCall{InstanceID: req.PluginInstanceID, DeviceID: req.DeviceID})
	r.mu.Unlock()
	return &driver.CloseDeviceResponse{PluginInstanceID: req.PluginInstanceID, DeviceID: req.DeviceID, Status: status.New()}, nil
}

func (r *fleetRecorder) Watch(_ context.Context, req *driver.WatchRequest) (driver.DriverMessageStream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.watches = append(r.watches, fleetWatchCall{
		InstanceID: req.PluginInstanceID,
		DeviceIDs:  append([]string(nil), req.DeviceIDs...),
	})
	s := newFleetWatchStream()
	r.streams[req.PluginInstanceID] = s
	return s, nil
}

func (r *fleetRecorder) Execute(_ context.Context, req *driver.ExecuteRequest) (*driver.ExecuteResponse, error) {
	r.mu.Lock()
	if err := r.executeErrOn[req.PluginInstanceID]; err != nil {
		r.mu.Unlock()
		return nil, err
	}
	state := driver.CommandStateSucceeded
	if v, ok := r.executeStateOn[req.PluginInstanceID]; ok {
		state = v
	}
	r.commandSeq++
	commandID := fmt.Sprintf("cmd-%d", r.commandSeq)
	r.execs = append(r.execs, fleetExecuteCall{
		InstanceID:     req.PluginInstanceID,
		DeviceID:       req.DeviceID,
		Action:         req.Action,
		ArgsJSON:       req.ArgsJSON,
		IdempotencyKey: req.IdempotencyKey,
		CommandID:      commandID,
	})
	r.mu.Unlock()
	resp := &driver.ExecuteResponse{
		CommandID:      commandID,
		IdempotencyKey: req.IdempotencyKey,
		State:          state,
	}
	if state == driver.CommandStateSucceeded {
		resp.Status = status.New()
	} else {
		resp.Status = status.Errorf(status.CodeInternal, "device rejected action %q", req.Action)
	}
	return resp, nil
}

func (r *fleetRecorder) Health(_ context.Context) (*driver.HealthResponse, error) {
	return &driver.HealthResponse{State: driver.HealthStateServing}, nil
}

func (r *fleetRecorder) Shutdown(_ context.Context, _ *driver.ShutdownRequest) (*driver.ShutdownResponse, error) {
	return &driver.ShutdownResponse{Status: status.New()}, nil
}

// ---- fleetRecorder 只读访问器与故障注入 ----

func (r *fleetRecorder) stats() (initializeCalls, describeCalls int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initializeCalls, r.describeCalls
}

func (r *fleetRecorder) opensSnapshot() []fleetOpenCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fleetOpenCall(nil), r.opens...)
}

func (r *fleetRecorder) closesSnapshot() []fleetCloseCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fleetCloseCall(nil), r.closes...)
}

func (r *fleetRecorder) watchesSnapshot() []fleetWatchCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fleetWatchCall(nil), r.watches...)
}

func (r *fleetRecorder) executesSnapshot() []fleetExecuteCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fleetExecuteCall(nil), r.execs...)
}

func (r *fleetRecorder) configOf(instanceID string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.configs[instanceID]
	return v, ok
}

func (r *fleetRecorder) hasStream(instanceID string) bool {
	r.mu.Lock()
	s := r.streams[instanceID]
	r.mu.Unlock()
	return s != nil && s.alive()
}

// publish 向指定实例的当前 Watch 流投递一条消息（模拟设备侧上报）。
func (r *fleetRecorder) publish(instanceID string, msg *driver.DriverMessage) bool {
	r.mu.Lock()
	s := r.streams[instanceID]
	r.mu.Unlock()
	if s == nil {
		return false
	}
	return s.send(msg)
}

// terminateStream 让指定实例的 Watch 流自然终止（模拟拔线/设备侧断流）。
func (r *fleetRecorder) terminateStream(instanceID string) {
	r.mu.Lock()
	s := r.streams[instanceID]
	r.mu.Unlock()
	if s != nil {
		s.terminate()
	}
}

func (r *fleetRecorder) setExecuteState(instanceID string, state driver.CommandState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executeStateOn[instanceID] = state
}

func (r *fleetRecorder) setExecuteErr(instanceID string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executeErrOn[instanceID] = err
}

func (r *fleetRecorder) clearExecuteFaults(instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.executeStateOn, instanceID)
	delete(r.executeErrOn, instanceID)
}

// ---- Watch 流替身 ----

type fleetWatchStream struct {
	mu     sync.Mutex
	ch     chan *driver.DriverMessage
	closed bool
}

func newFleetWatchStream() *fleetWatchStream {
	return &fleetWatchStream{ch: make(chan *driver.DriverMessage, 32)}
}

func (s *fleetWatchStream) Recv(ctx context.Context) (*driver.DriverMessage, error) {
	select {
	case msg, ok := <-s.ch:
		if !ok {
			return nil, errors.New("watch stream terminated")
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *fleetWatchStream) Cancel(context.Context) error {
	s.terminate()
	return nil
}

func (s *fleetWatchStream) send(msg *driver.DriverMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.ch <- msg:
		return true
	default:
		return false
	}
}

func (s *fleetWatchStream) terminate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

func (s *fleetWatchStream) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

// ---- PluginHost 替身 ----

// fleetHost 只认 driver ID → 会话，不含任何设备语义：这是「插件把设备带进平台」的最小面。
type fleetHost struct {
	driverID string
	rec      *fleetRecorder
}

func (h *fleetHost) Start(context.Context) error { return nil }

func (h *fleetHost) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (h *fleetHost) DriverIDs() ([]string, error) { return []string{h.driverID}, nil }

func (h *fleetHost) DriverClient(driverID string) (driver.DriverClient, error) {
	if driverID != h.driverID {
		return nil, fmt.Errorf("fleetHost: 未安装的 driver %q", driverID)
	}
	return h.rec, nil
}

// ---- 事件收集 ----

type fleetEvents struct {
	mu sync.Mutex
	by map[string][]device.Event
}

func newFleetEvents() *fleetEvents { return &fleetEvents{by: map[string][]device.Event{}} }

func (e *fleetEvents) sink(deviceID string) func(device.Event) {
	return func(ev device.Event) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.by[deviceID] = append(e.by[deviceID], ev)
	}
}

func (e *fleetEvents) of(deviceID string) []device.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]device.Event(nil), e.by[deviceID]...)
}

// ---- 测试夹具 ----

type fleetFixture struct {
	rec   *fleetRecorder
	host  *fleetHost
	ad    *externalAdapter
	devs  map[string]device.Device
	ev    *fleetEvents
	nodes []fleetNode

	seqMu sync.Mutex
	seq   uint64
}

// openFleet 用一个 generic driver ID 打开 3 台设备，并等各自的 Watch 流建立。
func openFleet(t *testing.T) *fleetFixture {
	t.Helper()
	rec := newFleetRecorder()
	host := &fleetHost{driverID: fleetDriverID, rec: rec}
	f := &fleetFixture{
		rec:   rec,
		host:  host,
		ad:    newExternalAdapter(host, fleetDriverID),
		devs:  map[string]device.Device{},
		ev:    newFleetEvents(),
		nodes: fleetNodes(),
	}
	ctx := context.Background()
	for _, n := range f.nodes {
		dev, err := f.ad.Open(ctx, n.config(), f.ev.sink(n.id))
		if err != nil {
			t.Fatalf("open %s: %v", n.id, err)
		}
		if dev.ID() != n.id {
			t.Fatalf("device.ID() = %q, want %q", dev.ID(), n.id)
		}
		f.devs[n.id] = dev
	}
	for _, n := range f.nodes {
		f.waitStream(t, n.instanceID())
	}
	t.Cleanup(func() {
		for _, d := range f.devs {
			_ = d.Close()
		}
	})
	return f
}

func (f *fleetFixture) waitStream(t *testing.T, instanceID string) {
	t.Helper()
	waitFor(t, 15*time.Second, func() bool { return f.rec.hasStream(instanceID) })
}

func (f *fleetFixture) publish(t *testing.T, n fleetNode, union driver.DriverMessageUnion) {
	t.Helper()
	f.seqMu.Lock()
	f.seq++
	seq := f.seq
	f.seqMu.Unlock()
	msg := &driver.DriverMessage{
		PluginInstanceID: n.instanceID(),
		Sequence:         seq,
		SchemaVersion:    driver.SchemaVersion,
		DeviceID:         n.id,
		Union:            union,
	}
	if !f.rec.publish(n.instanceID(), msg) {
		t.Fatalf("无法向实例 %s 的 Watch 流投递消息（流不存在或已终止）", n.instanceID())
	}
}

// publishOnline 走完整的设备上线序列：DeviceUpsert → EntityUpsert → Observation。
func (f *fleetFixture) publishOnline(t *testing.T, n fleetNode, value float64) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	f.publish(t, n, &driver.DeviceUpsert{Device: driver.Device{
		DeviceID:     n.id,
		ExternalID:   n.port,
		Manufacturer: "generic-vendor",
		Model:        "generic-model",
		Status:       driver.DeviceStatusOnline,
		DisplayName:  n.name,
	}})
	f.publish(t, n, &driver.EntityUpsert{Entity: driver.Entity{
		EntityID:     fleetEntityProbe,
		DeviceID:     n.id,
		UniqueKey:    fleetEntityProbe,
		Name:         "Probe",
		Category:     driver.EntityCategorySensor,
		Capabilities: []string{fleetCapabilityProbe},
	}})
	f.publishValue(t, n, value, now)
}

func (f *fleetFixture) publishValue(t *testing.T, n fleetNode, value float64, observedAt string) {
	t.Helper()
	if observedAt == "" {
		observedAt = time.Now().UTC().Format(time.RFC3339)
	}
	f.publish(t, n, &driver.Observation{
		EntityID:   fleetEntityProbe,
		Capability: fleetCapabilityProbe,
		Property:   "value",
		Value:      driver.Value{Kind: driver.ValueNumber, NumberValue: value},
		ObservedAt: observedAt,
		Quality:    "good",
	})
}

func (f *fleetFixture) publishEvent(t *testing.T, n fleetNode, eventType string) {
	t.Helper()
	f.publish(t, n, &driver.Event{
		EntityID:   fleetEntityRelay,
		DeviceID:   n.id,
		EventType:  eventType,
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func (f *fleetFixture) waitValue(t *testing.T, deviceID string, want float64) {
	t.Helper()
	waitFor(t, 15*time.Second, func() bool {
		st := f.devs[deviceID].Snapshot()
		v, ok := st.Raw[fleetEntityProbe+".value"].(float64)
		return st.Online && ok && v == want
	})
}

// ---- 通用小工具 ----

func chanClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func fleetSameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

// fleetSend 通过 Edge 使用的 ResultSender 契约下发命令（带设备 ACK 结果）。
func fleetSend(t *testing.T, f *fleetFixture, deviceID string, cmdID int64, cmd, args string) (string, error) {
	t.Helper()
	rs, ok := f.devs[deviceID].(ResultSender)
	if !ok {
		t.Fatalf("外部设备 %s 未实现 ResultSender：设备 ACK 结果无法回到 Edge", deviceID)
	}
	return rs.SendWithResult(context.Background(), device.Command{ID: cmdID, Cmd: cmd, Args: args})
}

var (
	_ driver.DriverClient     = (*fleetRecorder)(nil)
	_ PluginHost              = (*fleetHost)(nil)
	_ ResultSender            = (*externalDevice)(nil)
	_ device.DescriptorSource = (*externalDevice)(nil)
)

// ---- 故障注入辅助（方法可分散定义） ----

func (r *fleetRecorder) setOpenStatus(instanceID string, st *driver.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.openStatusOn[instanceID] = st
}

func (r *fleetRecorder) setOpenErr(instanceID string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.openErrOn[instanceID] = err
}

func fleetPublishOnce(t *testing.T, rec *fleetRecorder, instanceID, deviceID string, union driver.DriverMessageUnion) {
	t.Helper()
	msg := &driver.DriverMessage{
		PluginInstanceID: instanceID,
		Sequence:         1,
		SchemaVersion:    driver.SchemaVersion,
		DeviceID:         deviceID,
		Union:            union,
	}
	if !rec.publish(instanceID, msg) {
		t.Fatalf("无法向实例 %s 的 Watch 流投递消息", instanceID)
	}
}

// TestExternalDriverFleetOpensThreeIsolatedDevices 验证：一份外部 Driver 贡献
// 能在 Core 不做任何设备特例的前提下，独立打开 3 台设备——每台一个逻辑实例、
// 一份独立的连接提示、一条只订阅自己的 Watch 流，观测与事件互不串线。
func TestExternalDriverFleetOpensThreeIsolatedDevices(t *testing.T) {
	f := openFleet(t)

	// 命令白名单来自插件 Describe（设备无关），且解析一次后缓存。
	want := []string{"sample", "switch"}
	if got := f.ad.SupportedCommands(); !fleetSameStrings(got, want) {
		t.Fatalf("SupportedCommands() = %v, want %v（白名单必须来自插件自述）", got, want)
	}
	if got := f.ad.SupportedCommands(); !fleetSameStrings(got, want) {
		t.Fatalf("第二次 SupportedCommands() = %v, want %v", got, want)
	}
	initCalls, describeCalls := f.rec.stats()
	if initCalls < len(f.nodes) {
		t.Fatalf("Initialize 调用数 = %d, want >= %d（每次解析会话都要握手）", initCalls, len(f.nodes))
	}
	if describeCalls != 1 {
		t.Fatalf("Describe 调用数 = %d, want 1（白名单应缓存，不重复拉取）", describeCalls)
	}

	// OpenDevice：一台设备一个逻辑实例 + 独立连接提示。
	opens := f.rec.opensSnapshot()
	if len(opens) != len(f.nodes) {
		t.Fatalf("OpenDevice 调用数 = %d, want %d: %+v", len(opens), len(f.nodes), opens)
	}
	byInstance := map[string]fleetOpenCall{}
	for _, o := range opens {
		if _, dup := byInstance[o.InstanceID]; dup {
			t.Fatalf("逻辑实例 %q 被重复打开（设备身份未隔离）", o.InstanceID)
		}
		byInstance[o.InstanceID] = o
	}
	for _, n := range f.nodes {
		o, ok := byInstance[n.instanceID()]
		if !ok {
			t.Fatalf("缺少实例 %q 的 OpenDevice 记录: %+v", n.instanceID(), opens)
		}
		if o.DeviceID != n.id {
			t.Errorf("实例 %q 的 DeviceID = %q, want %q", n.instanceID(), o.DeviceID, n.id)
		}
		wantHints := map[string]string{
			"port": n.port,
			"baud": strconv.Itoa(n.baud),
			"name": n.name,
			"role": n.role,
		}
		for k, v := range wantHints {
			if o.Hints[k] != v {
				t.Errorf("设备 %s 的 hint %q = %q, want %q", n.id, k, o.Hints[k], v)
			}
		}
		// 串线检查：任何兄弟设备的物理绑定都不得出现在本设备的 hints 里。
		for _, other := range f.nodes {
			if other.id == n.id {
				continue
			}
			for k, v := range o.Hints {
				if v == other.port || v == other.name || v == other.role {
					t.Errorf("设备 %s 的 hint %q=%q 混入了 %s 的绑定", n.id, k, v, other.id)
				}
			}
		}
		// ConfigureInstance 必须携带同一台设备的本地绑定。
		raw, ok := f.rec.configOf(n.instanceID())
		if !ok {
			t.Fatalf("实例 %q 未收到 ConfigureInstance", n.instanceID())
		}
		var cfg map[string]any
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("实例 %q 的 config 不是合法 JSON: %v", n.instanceID(), err)
		}
		if cfg["device_id"] != n.id || cfg["port"] != n.port {
			t.Errorf("实例 %q 的 config = %s（device_id/port 与设备不匹配）", n.instanceID(), raw)
		}
	}

	// Watch：一台一条流，且只订阅自己。
	watches := f.rec.watchesSnapshot()
	if len(watches) != len(f.nodes) {
		t.Fatalf("Watch 调用数 = %d, want %d: %+v", len(watches), len(f.nodes), watches)
	}
	for _, w := range watches {
		if len(w.DeviceIDs) != 1 {
			t.Fatalf("实例 %q 的 Watch 订阅了 %d 台设备（应一台一条流）: %v", w.InstanceID, len(w.DeviceIDs), w.DeviceIDs)
		}
		if want := strings.TrimPrefix(w.InstanceID, fleetDriverID+"/"); w.DeviceIDs[0] != want {
			t.Errorf("实例 %q 的 Watch 订阅 %q, want %q", w.InstanceID, w.DeviceIDs[0], want)
		}
	}

	// 观测隔离：每台设备只看到自己流上的值。
	for i, n := range f.nodes {
		f.publishOnline(t, n, float64(i+1))
	}
	for i, n := range f.nodes {
		f.waitValue(t, n.id, float64(i+1))
		st := f.devs[n.id].Snapshot()
		if st.Raw["status"] != "online" {
			t.Errorf("设备 %s 的 status = %v, want online", n.id, st.Raw["status"])
		}
		if len(st.Raw) != 2 {
			t.Errorf("设备 %s 的 state.raw 含多余键（疑似串线）: %v", n.id, st.Raw)
		}
		src, ok := f.devs[n.id].(device.DescriptorSource)
		if !ok {
			t.Fatalf("设备 %s 未实现 DescriptorSource（Capability-driven UI 的前提）", n.id)
		}
		desc := src.Descriptor()
		if desc.DeviceID != n.id || desc.ExternalID != n.port {
			t.Errorf("设备 %s 的 Descriptor 身份 = %+v", n.id, desc)
		}
		if len(desc.Entities) != 1 {
			t.Fatalf("设备 %s 的实体数 = %d, want 1: %+v", n.id, len(desc.Entities), desc.Entities)
		}
		ent := desc.Entities[0]
		if ent.EntityID != fleetEntityProbe || ent.Category != model.EntitySensor {
			t.Errorf("设备 %s 的实体 = %+v", n.id, ent)
		}
		if len(ent.Capabilities) != 1 || ent.Capabilities[0] != fleetCapabilityProbe {
			t.Errorf("设备 %s 的能力声明 = %v, want [%s]", n.id, ent.Capabilities, fleetCapabilityProbe)
		}
		obs, ok := ent.Observations["value"]
		if !ok || obs.Value != float64(i+1) || obs.Quality != model.QualityGood {
			t.Errorf("设备 %s 的观测 = %+v, want value=%v", n.id, obs, float64(i+1))
		}
	}

	// 事件隔离：只有目标设备的回调被触发。
	f.publishEvent(t, f.nodes[1], "relay.changed")
	waitFor(t, 15*time.Second, func() bool { return len(f.ev.of("node-b")) == 1 })
	if got := f.ev.of("node-b")[0].Type; got != "relay.changed" {
		t.Errorf("node-b 事件类型 = %q, want relay.changed", got)
	}
	if n := len(f.ev.of("node-a")) + len(f.ev.of("node-c")); n != 0 {
		t.Errorf("兄弟设备收到了 %d 条不属于自己的事件", n)
	}

	// 未安装的 driver：fail-closed，且不得产生任何设备副作用。
	ghost := newExternalAdapter(f.host, "io.test/not-installed")
	if _, err := ghost.Open(context.Background(), device.Config{ID: "ghost-1"}, nil); err == nil {
		t.Fatal("未安装的 driver 必须 Open 失败")
	} else if !strings.Contains(err.Error(), "not-installed") {
		t.Fatalf("错误应指明缺失的 driver: %v", err)
	}
	if len(f.rec.opensSnapshot()) != len(f.nodes) {
		t.Fatalf("失败的 Open 不得产生 OpenDevice 记录: %+v", f.rec.opensSnapshot())
	}

	// 换一个完全不同的 driver ID，桥接行为必须一致：Core 不按 driver 名做特例。
	rec2 := newFleetRecorder()
	host2 := &fleetHost{driverID: fleetOtherDriverID, rec: rec2}
	ad2 := newExternalAdapter(host2, fleetOtherDriverID)
	other := device.Config{ID: "node-z", Name: "Node Z", Port: "tty-node-z", Baud: 19200}
	dev2, err := ad2.Open(context.Background(), other, nil)
	if err != nil {
		t.Fatalf("换 driver ID 后 Open 失败: %v", err)
	}
	t.Cleanup(func() { _ = dev2.Close() })
	wantInstance := fleetOtherDriverID + "/node-z"
	gotOpens := rec2.opensSnapshot()
	if len(gotOpens) != 1 || gotOpens[0].InstanceID != wantInstance || gotOpens[0].DeviceID != "node-z" {
		t.Fatalf("另一 driver 的 Open 记录 = %+v, want instance %q", gotOpens, wantInstance)
	}
	rs2, ok := dev2.(ResultSender)
	if !ok {
		t.Fatal("另一 driver 的设备未实现 ResultSender")
	}
	res2, err := rs2.SendWithResult(context.Background(), device.Command{ID: 900, Cmd: "switch", Args: "{}"})
	if err != nil || !strings.Contains(res2, "device ACK") {
		t.Fatalf("另一 driver 的命令结果 = %q, err = %v", res2, err)
	}
}

// TestExternalDriverFleetRoutesCommandsAndRequiresDeviceAck 验证：命令按设备路由到
// 正确的逻辑实例与 DeviceID，成功只来自设备侧 ACK；设备侧失败或传输失败必须冒泡，
// 且一台设备的失败不影响兄弟设备。
func TestExternalDriverFleetRoutesCommandsAndRequiresDeviceAck(t *testing.T) {
	f := openFleet(t)

	for i, n := range f.nodes {
		res, err := fleetSend(t, f, n.id, int64(101+i), "switch", fmt.Sprintf("{\"node\":%q}", n.id))
		if err != nil {
			t.Fatalf("设备 %s 命令失败: %v", n.id, err)
		}
		if !strings.Contains(res, "device ACK") || !strings.Contains(res, "action=switch") {
			t.Errorf("设备 %s 的执行结果摘要 = %q（应回显设备 ACK 与动作）", n.id, res)
		}
	}

	execs := f.rec.executesSnapshot()
	if len(execs) != len(f.nodes) {
		t.Fatalf("Execute 调用数 = %d, want %d: %+v", len(execs), len(f.nodes), execs)
	}
	perInstance := map[string]int{}
	for i, e := range execs {
		perInstance[e.InstanceID]++
		if want := strings.TrimPrefix(e.InstanceID, fleetDriverID+"/"); e.DeviceID != want {
			t.Errorf("Execute #%d 的 DeviceID = %q 与实例 %q 不一致（命令串线）", i, e.DeviceID, e.InstanceID)
		}
		if e.Action != "switch" {
			t.Errorf("Execute #%d 的 action = %q, want switch", i, e.Action)
		}
		if !strings.Contains(e.ArgsJSON, e.DeviceID) {
			t.Errorf("Execute #%d 的参数未随命令到达目标设备: %s", i, e.ArgsJSON)
		}
		wantKey := fmt.Sprintf("%d-switch", 101+i)
		if e.IdempotencyKey != wantKey {
			t.Errorf("Execute #%d 的幂等键 = %q, want %q", i, e.IdempotencyKey, wantKey)
		}
		if e.CommandID == "" {
			t.Errorf("Execute #%d 未带回设备命令 ID（无法做 ACK 关联）", i)
		}
	}
	for _, n := range f.nodes {
		if perInstance[n.instanceID()] != 1 {
			t.Errorf("实例 %q 收到 %d 条命令, want 1", n.instanceID(), perInstance[n.instanceID()])
		}
	}

	// 设备侧拒绝（State != succeeded）必须是错误：串口写成功不等于执行成功。
	f.rec.setExecuteState(f.nodes[1].instanceID(), driver.CommandStateFailed)
	if _, err := fleetSend(t, f, "node-b", 201, "sample", ""); err == nil {
		t.Fatal("设备侧执行失败必须冒泡为错误")
	} else if !strings.Contains(err.Error(), "sample") {
		t.Errorf("错误应指明失败动作: %v", err)
	}
	// 传输层失败同样必须是错误。
	f.rec.setExecuteErr(f.nodes[2].instanceID(), errors.New("plugin transport gone"))
	if _, err := fleetSend(t, f, "node-c", 202, "sample", ""); err == nil {
		t.Fatal("传输失败必须冒泡为错误")
	} else if !strings.Contains(err.Error(), "plugin transport gone") {
		t.Errorf("错误应保留根因: %v", err)
	}
	// 超时/取消态也不得被当成成功。
	f.rec.setExecuteState(f.nodes[1].instanceID(), driver.CommandStateTimedOut)
	if _, err := fleetSend(t, f, "node-b", 203, "sample", ""); err == nil {
		t.Fatal("CommandStateTimedOut 不得被当成成功")
	}
	// 兄弟设备不受影响。
	if _, err := fleetSend(t, f, "node-a", 204, "sample", ""); err != nil {
		t.Fatalf("一台设备的失败不得影响兄弟设备: %v", err)
	}
	// 故障恢复后该设备重新可执行（无粘滞状态）。
	f.rec.clearExecuteFaults(f.nodes[1].instanceID())
	f.rec.clearExecuteFaults(f.nodes[2].instanceID())
	if _, err := fleetSend(t, f, "node-b", 205, "sample", ""); err != nil {
		t.Fatalf("node-b 恢复后应可执行: %v", err)
	}
	if _, err := fleetSend(t, f, "node-c", 206, "sample", ""); err != nil {
		t.Fatalf("node-c 恢复后应可执行: %v", err)
	}
}

// TestExternalDriverFleetLifecycleIsIsolated 验证：关闭一台设备或它的 Watch 流终止
// （拔线）只收敛它自己；兄弟设备保持在线、可观测、可命令，且不会被替它发 CloseDevice；
// 重新 Open 同一台设备会得到独立的新流。
func TestExternalDriverFleetLifecycleIsIsolated(t *testing.T) {
	f := openFleet(t)
	for i, n := range f.nodes {
		f.publishOnline(t, n, float64(i+1))
		f.waitValue(t, n.id, float64(i+1))
	}

	// 关闭 node-a。
	if err := f.devs["node-a"].Close(); err != nil {
		t.Fatalf("close node-a: %v", err)
	}
	waitFor(t, 15*time.Second, func() bool { return chanClosed(f.devs["node-a"].Done()) })
	closes := f.rec.closesSnapshot()
	if len(closes) != 1 || closes[0].InstanceID != f.nodes[0].instanceID() || closes[0].DeviceID != "node-a" {
		t.Fatalf("CloseDevice 记录 = %+v, want 只关闭 node-a 的实例", closes)
	}
	if chanClosed(f.devs["node-b"].Done()) || chanClosed(f.devs["node-c"].Done()) {
		t.Fatal("关闭一台设备不得关闭兄弟设备的生命周期")
	}
	if !f.rec.hasStream(f.nodes[1].instanceID()) || !f.rec.hasStream(f.nodes[2].instanceID()) {
		t.Fatal("兄弟设备的 Watch 流应保持存活")
	}
	// 兄弟设备仍可观测与命令。
	f.publishValue(t, f.nodes[2], 99, "")
	f.waitValue(t, "node-c", 99)
	if _, err := fleetSend(t, f, "node-c", 301, "switch", ""); err != nil {
		t.Fatalf("node-c 应仍可执行命令: %v", err)
	}
	if _, err := fleetSend(t, f, "node-b", 302, "switch", ""); err != nil {
		t.Fatalf("node-b 应仍可执行命令: %v", err)
	}

	// 拔线：node-b 的流自然终止，只收敛 node-b。
	f.rec.terminateStream(f.nodes[1].instanceID())
	waitFor(t, 15*time.Second, func() bool { return chanClosed(f.devs["node-b"].Done()) })
	if chanClosed(f.devs["node-c"].Done()) {
		t.Fatal("一台设备的流终止不得影响兄弟设备")
	}
	if _, err := fleetSend(t, f, "node-c", 303, "sample", ""); err != nil {
		t.Fatalf("流终止后 node-c 应仍可执行命令: %v", err)
	}
	f.publishValue(t, f.nodes[2], 123, "")
	f.waitValue(t, "node-c", 123)
	if closes := f.rec.closesSnapshot(); len(closes) != 1 {
		t.Fatalf("流终止不得替兄弟设备发 CloseDevice: %+v", closes)
	}

	// 重连：重新 Open node-b 得到独立新流，node-c 不受影响。
	reopened, err := f.ad.Open(context.Background(), f.nodes[1].config(), f.ev.sink("node-b"))
	if err != nil {
		t.Fatalf("重新打开 node-b: %v", err)
	}
	f.devs["node-b"] = reopened
	f.waitStream(t, f.nodes[1].instanceID())
	watches := f.rec.watchesSnapshot()
	var nodeBWatches int
	for _, w := range watches {
		if w.InstanceID == f.nodes[1].instanceID() {
			nodeBWatches++
			if len(w.DeviceIDs) != 1 || w.DeviceIDs[0] != "node-b" {
				t.Fatalf("重连后的 Watch 订阅 = %v, want [node-b]", w.DeviceIDs)
			}
		}
	}
	if nodeBWatches != 2 {
		t.Fatalf("node-b 的 Watch 次数 = %d, want 2（断流后重连应新建流）", nodeBWatches)
	}
	f.publishOnline(t, f.nodes[1], 7)
	f.waitValue(t, "node-b", 7)
	if _, err := fleetSend(t, f, "node-b", 304, "sample", ""); err != nil {
		t.Fatalf("重连后的 node-b 应可执行命令: %v", err)
	}
	f.publishValue(t, f.nodes[2], 124, "")
	f.waitValue(t, "node-c", 124)
	if chanClosed(f.devs["node-c"].Done()) {
		t.Fatal("node-b 重连不得影响 node-c")
	}
}

// TestExternalDriverFleetOpenFailureIsIsolated 验证错误配置/设备缺失的 fail-closed：
// 一台设备 Open 失败必须冒泡为错误并留下可诊断原因，兄弟设备照常上线与被控制。
func TestExternalDriverFleetOpenFailureIsIsolated(t *testing.T) {
	rec := newFleetRecorder()
	host := &fleetHost{driverID: fleetDriverID, rec: rec}
	ad := newExternalAdapter(host, fleetDriverID)
	nodes := fleetNodes()
	ctx := context.Background()

	// node-b：设备侧返回 NOT_FOUND（例如端口不存在）。
	rec.setOpenStatus(nodes[1].instanceID(), status.Errorf(status.CodeNotFound, "port %s not present", nodes[1].port))
	if _, err := ad.Open(ctx, nodes[1].config(), nil); err == nil {
		t.Fatal("设备侧 Open 失败必须冒泡为错误（不得静默上线）")
	} else if !strings.Contains(err.Error(), nodes[1].port) {
		t.Fatalf("错误应带上失败设备的具体原因: %v", err)
	}

	devs := map[string]device.Device{}
	t.Cleanup(func() {
		for _, d := range devs {
			_ = d.Close()
		}
	})
	for _, i := range []int{0, 2} {
		d, err := ad.Open(ctx, nodes[i].config(), nil)
		if err != nil {
			t.Fatalf("兄弟设备 %s 不应受失败影响: %v", nodes[i].id, err)
		}
		devs[nodes[i].id] = d
	}

	// 失败设备不得留下 Watch 流或 CloseDevice 记录。
	for _, w := range rec.watchesSnapshot() {
		if w.InstanceID == nodes[1].instanceID() {
			t.Fatalf("Open 失败不得启动 Watch 流: %+v", w)
		}
	}
	for _, c := range rec.closesSnapshot() {
		if c.DeviceID == nodes[1].id {
			t.Fatalf("Open 失败不得产生 CloseDevice: %+v", c)
		}
	}

	// 兄弟设备照常上报与被控制。
	for _, i := range []int{0, 2} {
		n := nodes[i]
		waitFor(t, 15*time.Second, func() bool { return rec.hasStream(n.instanceID()) })
		fleetPublishOnce(t, rec, n.instanceID(), n.id, &driver.DeviceUpsert{Device: driver.Device{
			DeviceID: n.id, ExternalID: n.port, Status: driver.DeviceStatusOnline, DisplayName: n.name,
		}})
		waitFor(t, 15*time.Second, func() bool { return devs[n.id].Snapshot().Online })
		rs, ok := devs[n.id].(ResultSender)
		if !ok {
			t.Fatalf("设备 %s 未实现 ResultSender", n.id)
		}
		if _, err := rs.SendWithResult(ctx, device.Command{ID: int64(400 + i), Cmd: "sample"}); err != nil {
			t.Fatalf("设备 %s 应可执行命令: %v", n.id, err)
		}
	}

	// 传输层 Open 失败同样 fail-closed，且不影响已在线设备。
	rec.setOpenErr(fleetDriverID+"/node-d", errors.New("plugin crashed during open"))
	if _, err := ad.Open(ctx, device.Config{ID: "node-d", Port: "tty-node-d", Baud: 9600}, nil); err == nil {
		t.Fatal("传输层 Open 失败必须冒泡为错误")
	} else if !strings.Contains(err.Error(), "plugin crashed during open") {
		t.Fatalf("错误应保留根因: %v", err)
	}
	for _, id := range []string{"node-a", "node-c"} {
		rs := devs[id].(ResultSender)
		if _, err := rs.SendWithResult(ctx, device.Command{ID: 500, Cmd: "switch"}); err != nil {
			t.Fatalf("已在线设备 %s 应不受新设备失败影响: %v", id, err)
		}
	}
}
