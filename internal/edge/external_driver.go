package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
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

	mu        sync.Mutex
	actions   []string
	caps      []model.Capability
	described bool
}

// describeTimeout 限定一次 Describe RPC：插件卡死时不得把 Edge 的上报回调一起挂住。
const describeTimeout = 10 * time.Second

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

func externalDeviceInstanceID(driverID, deviceID string) string { return driverID + "/" + deviceID }

// SupportedCommands 返回 Describe 里声明的全部 action 名（命令白名单）。
// 外部 driver 的轮询/对时由 driver 自身在 Watch 循环内完成，因此这里不额外注入
// 生命周期命令；失败时返回空集，edge 会按「无可轮询命令」安全降级。
func (a *externalAdapter) SupportedCommands() []string {
	actions, _ := a.describeOnce()
	return actions
}

// Capabilities 返回 Describe 里声明的 Capability 文档，由 Edge 上报给 Server
// （/api/capabilities 与前端 Schema 驱动 UI 的事实源）。设备无关：只按 Driver
// Protocol 契约搬运字段，Core 不认识任何具体硬件。
func (a *externalAdapter) Capabilities() []model.Capability {
	_, caps := a.describeOnce()
	return caps
}

// describeOnce 缓存一次成功的 Describe 结果（命令白名单 + Capability 文档同源，
// 避免两次 RPC）。失败**不**缓存：插件后启动时下一次调用仍会重试。
func (a *externalAdapter) describeOnce() ([]string, []model.Capability) {
	a.mu.Lock()
	if a.described {
		actions, caps := a.actions, a.caps
		a.mu.Unlock()
		return actions, caps
	}
	a.mu.Unlock()

	actions, caps, ok := a.resolveDescriptor()
	if !ok {
		return nil, nil
	}
	a.mu.Lock()
	a.actions, a.caps, a.described = actions, caps, true
	a.mu.Unlock()
	return actions, caps
}

func (a *externalAdapter) resolveDescriptor() ([]string, []model.Capability, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), describeTimeout)
	defer cancel()
	cli, err := a.client(ctx)
	if err != nil {
		return nil, nil, false
	}
	desc, err := cli.Describe(ctx)
	if err != nil {
		return nil, nil, false
	}
	var actions []string
	for _, c := range desc.Capabilities {
		for _, act := range c.Actions {
			if strings.TrimSpace(act.Name) != "" {
				actions = append(actions, act.Name)
			}
		}
	}
	return actions, capabilityDocs(a.driverID, desc.Capabilities), true
}

// capabilityDocs 把 Driver Protocol v1 的 CapabilityDescriptor 转成 Core 的
// model.Capability 文档。契约字段一一搬运；非法文档跳过并记 warn（绝不上报脏数据，
// 否则 Server 侧 Validate 会整批丢弃，反而更难排障）。
func capabilityDocs(driverID string, in []driver.CapabilityDescriptor) []model.Capability {
	out := make([]model.Capability, 0, len(in))
	for _, c := range in {
		id := strings.TrimSpace(c.ID)
		if id == "" {
			continue
		}
		doc := model.Capability{
			APIVersion: model.CapabilityAPIVersion,
			Kind:       model.CapabilityKind,
			Metadata:   model.CapabilityMetadata{ID: id, Version: capabilityVersion(id), Title: c.Title},
		}
		if len(c.Properties) > 0 {
			props := make(map[string]model.Property, len(c.Properties))
			for _, p := range c.Properties {
				name := strings.TrimSpace(p.Name)
				if name == "" {
					continue
				}
				props[name] = model.Property{
					Type:    p.Type,
					Unit:    p.Unit,
					Access:  model.PropertyAccess(p.Access),
					Quality: qualities(p.Quality),
				}
			}
			doc.Spec.Properties = props
		}
		if len(c.Events) > 0 {
			evs := make(map[string]model.EventDecl, len(c.Events))
			for _, ev := range c.Events {
				name := strings.TrimSpace(ev.Name)
				if name == "" {
					continue
				}
				evs[name] = model.EventDecl{PayloadSchema: schemaJSON(ev.PayloadSchemaJSON)}
			}
			doc.Spec.Events = evs
		}
		if len(c.Actions) > 0 {
			acts := make(map[string]model.ActionDecl, len(c.Actions))
			for _, act := range c.Actions {
				name := strings.TrimSpace(act.Name)
				if name == "" {
					continue
				}
				acts[name] = model.ActionDecl{InputSchema: schemaJSON(act.InputSchemaJSON)}
			}
			doc.Spec.Actions = acts
		}
		if err := doc.Validate(); err != nil {
			slog.Warn("driver capability doc invalid, skipped",
				"driver", driverID, "capability", id, "err", err)
			continue
		}
		out = append(out, doc)
	}
	return out
}

// capabilityVersion 从 "<publisher>/capability/<name>@<version>" 取版本号；缺失/非法为 0。
func capabilityVersion(id string) int {
	i := strings.LastIndex(id, "@")
	if i < 0 || i == len(id)-1 {
		return 0
	}
	v, err := strconv.Atoi(id[i+1:])
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func qualities(in []string) []model.Quality {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.Quality, 0, len(in))
	for _, q := range in {
		out = append(out, model.Quality(q))
	}
	return out
}

// schemaJSON 解析插件给的 JSON Schema 字符串；空或非法一律 nil（文档可选字段）。
func schemaJSON(s string) map[string]any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
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
	instanceID := externalDeviceInstanceID(a.driverID, cfg.ID)
	cresp, err := cli.ConfigureInstance(ctx, &driver.ConfigureInstanceRequest{
		PluginInstanceID: instanceID,
		Config:           cfgJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("external driver %q configure instance: %w", a.driverID, err)
	}
	if cresp.Status != nil && !cresp.Status.IsOK() {
		return nil, fmt.Errorf("external driver %q configure instance: %v", a.driverID, cresp.Status)
	}
	return openExternalDevice(ctx, cli, instanceID, cfg, onEvent)
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

func externalConnectionHints(cfg device.Config) map[string]string {
	hints := map[string]string{}
	if cfg.Port != "" {
		hints["port"] = cfg.Port
	}
	if cfg.Baud > 0 {
		hints["baud"] = strconv.Itoa(cfg.Baud)
	}
	if cfg.Name != "" {
		hints["name"] = cfg.Name
	}
	for k, v := range cfg.Extra {
		if k != "" && v != "" {
			hints[k] = v
		}
	}
	return hints
}

func openExternalDevice(ctx context.Context, cli driver.DriverClient, instanceID string, cfg device.Config, onEvent func(device.Event)) (*externalDevice, error) {
	devID := cfg.ID
	if devID == "" {
		return nil, fmt.Errorf("external driver: device id 必填")
	}
	resp, err := cli.OpenDevice(ctx, &driver.OpenDeviceRequest{
		PluginInstanceID: instanceID,
		DeviceID:         devID,
		ConnectionHints:  externalConnectionHints(cfg),
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
			onEvent(device.Event{Type: u.EventType, EntityID: u.EntityID, At: at})
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

// Send 执行命令；成功只来自 Driver 返回的真实设备 ACK 结果。
func (d *externalDevice) Send(ctx context.Context, c device.Command) error {
	_, err := d.SendWithResult(ctx, c)
	return err
}

// SendWithResult 把 Driver 已确认的设备结果返回给 Edge command ACK。
func (d *externalDevice) SendWithResult(ctx context.Context, c device.Command) (string, error) {
	resp, err := d.cli.Execute(ctx, &driver.ExecuteRequest{
		PluginInstanceID: d.instanceID,
		DeviceID:         d.id,
		IdempotencyKey:   commandID(c),
		EntityID:         "",
		Action:           c.Cmd,
		ArgsJSON:         c.Args,
	})
	if err != nil {
		return "", err
	}
	if resp.Status != nil && !resp.Status.IsOK() {
		return "", fmt.Errorf("external driver execute %q: %v", c.Cmd, resp.Status)
	}
	if resp.State != driver.CommandStateSucceeded {
		return "", fmt.Errorf("external driver execute %q: state %v", c.Cmd, resp.State)
	}
	return fmt.Sprintf("device ACK action=%s command_id=%s", c.Cmd, resp.CommandID), nil
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
