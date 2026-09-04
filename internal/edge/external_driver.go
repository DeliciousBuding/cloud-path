package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/device"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
)

// externalAdapter 把已安装的 Driver Protocol v1 插件桥接进 edge 监督循环使用的
// 进程内 device.Adapter 模型。外部 driver 的实例配置（串口等）由 Server desired
// state 注入，不经 edge.yaml；edge 只负责按 driver ID 找到会话并转发命令/订阅观测。
type externalAdapter struct {
	host     PluginHost
	driverID string
	launchID string

	mu          sync.Mutex
	actions     []string
	actionsDone bool
}

// externalInstanceConfig 把 edge.yaml 的本地物理绑定编码为插件实例配置。
func externalInstanceConfig(cfg device.Config) ([]byte, error) {
	return json.Marshal(map[string]any{
		"device_id": cfg.ID,
		"name":      cfg.Name,
		"port":      cfg.Port,
		"baud":      cfg.Baud,
		"extra":     cfg.Extra,
	})
}

func newExternalAdapter(host PluginHost, driverID string) *externalAdapter {
	return &externalAdapter{host: host, driverID: driverID, launchID: fmt.Sprintf("edge-ext-%d", time.Now().UnixNano())}
}

// initializeDriverClient 执行 Driver Protocol v1 的 Initialize 握手。插件 host
// 只建立传输与 stdout/socket 握手，不代打协议层 Initialize；Watch 在插件侧要求
// initialized 为真，因此桥接层必须在首个协议调用前完成握手。
func initializeDriverClient(ctx context.Context, cli driver.DriverClient, launchID string) error {
	resp, err := cli.Initialize(ctx, &driver.InitializeRequest{
		ProtocolVersion:           driver.ProtocolVersion,
		SupportedProtocolVersions: []uint32{driver.ProtocolVersion},
		LaunchID:                  launchID,
		RuntimeType:               "edge-external-driver",
	})
	if err != nil {
		return err
	}
	if resp.Status != nil && !resp.Status.IsOK() {
		return fmt.Errorf("driver initialize: %v", resp.Status)
	}
	return nil
}

// client 解析当前会话的 DriverClient 并确保完成协议层 Initialize。插件进程重启后
// DriverClient 会指向新会话，因此每次解析都重新握手（Initialize 幂等）。
func (a *externalAdapter) client(ctx context.Context) (driver.DriverClient, error) {
	cli, err := a.host.DriverClient(a.driverID)
	if err != nil {
		return nil, fmt.Errorf("external driver %q: %w", a.driverID, err)
	}
	if err := initializeDriverClient(ctx, cli, a.launchID); err != nil {
		return nil, fmt.Errorf("external driver %q initialize: %w", a.driverID, err)
	}
	return cli, nil
}

func (a *externalAdapter) Name() string { return a.driverID }

// SupportedCommands 返回 Describe 里声明的全部 action 名（命令白名单）。
// 外部 driver 的轮询/对时由 driver 自身在 Watch 循环内完成，因此这里不额外注入
// 生命周期命令；失败时返回空集，edge 会按「无可轮询命令」安全降级。
func (a *externalAdapter) SupportedCommands() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.actionsDone {
		return a.actions
	}
	a.actionsDone = true
	a.actions = a.resolveActions()
	return a.actions
}

func (a *externalAdapter) resolveActions() []string {
	cli, err := a.client(context.Background())
	if err != nil {
		return nil
	}
	desc, err := cli.Describe(context.Background())
	if err != nil {
		return nil
	}
	var out []string
	for _, c := range desc.Capabilities {
		for _, act := range c.Actions {
			if strings.TrimSpace(act.Name) != "" {
				out = append(out, act.Name)
			}
		}
	}
	return out
}

// Open 获取当前会话的 DriverClient，先把 edge.yaml 的本地物理绑定注入插件实例配置，
// 再打开设备并启动观测订阅。这样干净机器只需在 edge.yaml 写 adapter/port，无需预先在
// Server 手写插件实例 config；插件 enable/version/lock 仍由 Server desired state 权威。
func (a *externalAdapter) Open(ctx context.Context, cfg device.Config, onEvent func(device.Event)) (device.Device, error) {
	cli, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	cfgJSON, err := externalInstanceConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("external driver %q encode config: %w", a.driverID, err)
	}
	cresp, err := cli.ConfigureInstance(ctx, &driver.ConfigureInstanceRequest{
		PluginInstanceID: a.driverID,
		Config:           cfgJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("external driver %q configure instance: %w", a.driverID, err)
	}
	if cresp.Status != nil && !cresp.Status.IsOK() {
		return nil, fmt.Errorf("external driver %q configure instance: %v", a.driverID, cresp.Status)
	}
	return openExternalDevice(ctx, cli, a.driverID, cfg, onEvent)
}

// externalDevice 是桥接后的单台外部设备：维护 Watch 流累积的实体与观测，
// 并转换为 edge 上报所需的 State / Descriptor。
type externalDevice struct {
	id         string
	instanceID string
	cli        driver.DriverClient

	mu        sync.Mutex
	info      driver.Device
	entities  map[string]driver.Entity
	obs       map[string]map[string]driver.Observation // entityID -> property -> observation
	updatedAt time.Time

	done     chan struct{}
	doneOnce sync.Once
	cancel   context.CancelFunc
}

func openExternalDevice(ctx context.Context, cli driver.DriverClient, instanceID string, cfg device.Config, onEvent func(device.Event)) (*externalDevice, error) {
	devID := cfg.ID
	if devID == "" {
		return nil, fmt.Errorf("external driver: device id 必填")
	}
	resp, err := cli.OpenDevice(ctx, &driver.OpenDeviceRequest{
		PluginInstanceID: instanceID,
		DeviceID:         devID,
	})
	if err != nil {
		return nil, err
	}
	if resp.Status != nil && !resp.Status.IsOK() {
		return nil, fmt.Errorf("external driver open device: %v", resp.Status)
	}

	watchCtx, cancel := context.WithCancel(context.Background())
	d := &externalDevice{
		id:         devID,
		instanceID: instanceID,
		cli:        cli,
		entities:   map[string]driver.Entity{},
		obs:        map[string]map[string]driver.Observation{},
		done:       make(chan struct{}),
		cancel:     cancel,
	}
	go d.watchLoop(watchCtx, onEvent)
	return d, nil
}

func (d *externalDevice) ID() string { return d.id }

func (d *externalDevice) Done() <-chan struct{} { return d.done }

func (d *externalDevice) Close() error {
	d.doneOnce.Do(func() { close(d.done) })
	if d.cancel != nil {
		d.cancel()
	}
	_, _ = d.cli.CloseDevice(context.Background(), &driver.CloseDeviceRequest{
		PluginInstanceID: d.instanceID,
		DeviceID:         d.id,
	})
	return nil
}

// watchLoop 消费 Watch 流：实体/观测/事件/设备状态。
func (d *externalDevice) watchLoop(ctx context.Context, onEvent func(device.Event)) {
	defer d.doneOnce.Do(func() { close(d.done) })
	stream, err := d.cli.Watch(ctx, &driver.WatchRequest{
		PluginInstanceID: d.instanceID,
		DeviceIDs:        []string{d.id},
	})
	if err != nil {
		return
	}
	for {
		msg, err := stream.Recv(ctx)
		if err != nil {
			return
		}
		d.applyMessage(msg, onEvent)
	}
}

func (d *externalDevice) applyMessage(msg *driver.DriverMessage, onEvent func(device.Event)) {
	switch u := msg.Union.(type) {
	case *driver.DeviceUpsert:
		d.mu.Lock()
		d.info = u.Device
		d.updatedAt = time.Now()
		d.mu.Unlock()
	case *driver.EntityUpsert:
		d.mu.Lock()
		if u.Removed {
			delete(d.entities, u.Entity.EntityID)
			delete(d.obs, u.Entity.EntityID)
		} else {
			d.entities[u.Entity.EntityID] = u.Entity
			if d.obs[u.Entity.EntityID] == nil {
				d.obs[u.Entity.EntityID] = map[string]driver.Observation{}
			}
		}
		d.updatedAt = time.Now()
		d.mu.Unlock()
	case *driver.Observation:
		d.mu.Lock()
		m := d.obs[u.EntityID]
		if m == nil {
			m = map[string]driver.Observation{}
			d.obs[u.EntityID] = m
		}
		m[u.Property] = *u
		d.updatedAt = time.Now()
		d.mu.Unlock()
	case *driver.Event:
		if onEvent != nil {
			at := time.Now()
			if t, err := time.Parse(time.RFC3339, u.OccurredAt); err == nil {
				at = t
			}
			onEvent(device.Event{Type: u.EventType, At: at})
		}
	}
}

// snapshotLocked 返回当前快照的浅拷贝（调用方持锁）。
func (d *externalDevice) snapshotLocked() (info driver.Device, entities map[string]driver.Entity, obs map[string]map[string]driver.Observation) {
	entities = make(map[string]driver.Entity, len(d.entities))
	for k, v := range d.entities {
		entities[k] = v
	}
	obs = make(map[string]map[string]driver.Observation, len(d.obs))
	for k, v := range d.obs {
		cp := make(map[string]driver.Observation, len(v))
		for kk, vv := range v {
			cp[kk] = vv
		}
		obs[k] = cp
	}
	return d.info, entities, obs
}

// Snapshot 构造 edge 上报的 State.Raw（扁平化观测，便于排障与旧面板兜底）。
func (d *externalDevice) Snapshot() device.State {
	d.mu.Lock()
	info, entities, obs := d.snapshotLocked()
	updated := d.updatedAt
	d.mu.Unlock()

	raw := map[string]any{"status": statusLabel(info.Status)}
	for _, e := range entities {
		for prop, o := range obs[e.EntityID] {
			raw[e.EntityID+"."+prop] = driverValueToAny(o.Value)
		}
	}
	return device.State{Online: info.Status == driver.DeviceStatusOnline, Raw: raw, UpdatedAt: updated}
}

// Descriptor 把 Watch 流累积的实体与观测转成 typed Descriptor。
func (d *externalDevice) Descriptor() model.Descriptor {
	d.mu.Lock()
	info, entities, obs := d.snapshotLocked()
	d.mu.Unlock()

	desc := model.Descriptor{
		DeviceID:     d.id,
		ExternalID:   info.ExternalID,
		Manufacturer: info.Manufacturer,
		Model:        info.Model,
		Status:       deviceStatus(info.Status),
		Entities:     make([]model.Entity, 0, len(entities)),
	}
	for _, e := range entities {
		me := model.Entity{
			EntityID:     e.EntityID,
			UniqueKey:    e.UniqueKey,
			Name:         e.Name,
			Category:     entityCategory(e.Category),
			Capabilities: append([]string(nil), e.Capabilities...),
			Observations: map[string]model.Observation{},
		}
		for prop, o := range obs[e.EntityID] {
			mo := model.Observation{
				EntityID:   o.EntityID,
				Capability: o.Capability,
				Property:   o.Property,
				Value:      driverValueToAny(o.Value),
				Quality:    model.Quality(o.Quality),
			}
			if t, err := time.Parse(time.RFC3339, o.ObservedAt); err == nil {
				mo.ObservedAt = t
			}
			me.Observations[prop] = mo
		}
		desc.Entities = append(desc.Entities, me)
	}
	return desc
}

// Send 执行命令：cmd.Cmd 即 Driver Protocol v1 的 action。
func (d *externalDevice) Send(ctx context.Context, c device.Command) error {
	resp, err := d.cli.Execute(ctx, &driver.ExecuteRequest{
		PluginInstanceID: d.instanceID,
		IdempotencyKey:   commandID(c),
		EntityID:         "",
		Action:           c.Cmd,
		ArgsJSON:         c.Args,
	})
	if err != nil {
		return err
	}
	if resp.Status != nil && !resp.Status.IsOK() {
		return fmt.Errorf("external driver execute %q: %v", c.Cmd, resp.Status)
	}
	if resp.State == driver.CommandStateFailed {
		return fmt.Errorf("external driver execute %q: state FAILED", c.Cmd)
	}
	return nil
}

func commandID(c device.Command) string {
	return fmt.Sprintf("%d-%s", c.ID, c.Cmd)
}

func statusLabel(s driver.DeviceStatus) string {
	switch s {
	case driver.DeviceStatusOnline:
		return "online"
	case driver.DeviceStatusOffline:
		return "offline"
	case driver.DeviceStatusUnavailable:
		return "unavailable"
	case driver.DeviceStatusDegraded:
		return "degraded"
	default:
		return "unspecified"
	}
}

func deviceStatus(s driver.DeviceStatus) model.DeviceStatus {
	switch s {
	case driver.DeviceStatusOnline:
		return model.DeviceOnline
	case driver.DeviceStatusOffline:
		return model.DeviceOffline
	case driver.DeviceStatusDegraded:
		return model.DeviceDegraded
	default:
		return model.DeviceUnavailable
	}
}

func entityCategory(c driver.EntityCategory) model.EntityCategory {
	switch c {
	case driver.EntityCategoryActuator:
		return model.EntityActuator
	case driver.EntityCategoryDiagnostic:
		return model.EntityDiagnostic
	case driver.EntityCategoryConfig:
		return model.EntityConfig
	default:
		return model.EntitySensor
	}
}

func driverValueToAny(v driver.Value) any {
	switch v.Kind {
	case driver.ValueNumber:
		return v.NumberValue
	case driver.ValueInt:
		return v.IntValue
	case driver.ValueBool:
		return v.BoolValue
	case driver.ValueJSON:
		return v.JSONValue
	case driver.ValueString:
		return v.StringValue
	default:
		return nil
	}
}

var _ device.Adapter = (*externalAdapter)(nil)
var _ device.Device = (*externalDevice)(nil)
var _ device.DescriptorSource = (*externalDevice)(nil)
