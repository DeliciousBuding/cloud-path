// Package demo 实现 CloudPath 的参考演示设备适配器（reference demo device）。
//
// # 诚实性红线
//
// 本适配器不对应任何真实硬件：不打开串口、不访问任何外设、不生成随机假数。
// 它上报的是**本进程内真实维护的状态**（运行时长、心跳计数、命令计数、可写回
// 的设定值与开关）；命令真实改变这些进程内状态并产生事件。设备类别在
// State.Raw、Descriptor 与 plugin.yaml 中始终标注为 reference demo device，
// 绝不允许被 UI 或上报误标成真实硬件。
//
// # 存在意义
//
// 让没有任何板子的电脑也能接入 CloudPath：Edge→Server→WebUI 的多设备、实时
// 状态、独立控制、断线重连全链路可以在纯软件下跑通与验收（无硬件依赖）。
//
// # 拆仓红线
//
// 与 examples/stcb 相同：本包只依赖 sdk/go/driverkit 与 sdk/go/model，
// 不 import 任何 internal/*（由 demo_test.go 的导入扫描锁定）。
package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
)

// Name 是注册的适配器名，同时是 plugin.yaml contributes.drivers 的贡献 ID。
const Name = "demo"

// Kind 是设备类别标注（诚实性红线）：出现在 State.Raw 与 Descriptor 观测中，
// 让任何消费方都能一眼看出这不是真实硬件。
const Kind = "reference-demo-device"

// 命令白名单。server 的命令校验与前端命令面板都以 SupportedCommands() 为
// 唯一事实源；本包不得在别处另立一份命令表。
const (
	CmdPing = "ping" // 存活探测：计数 +1 并产生 probed 事件
	CmdSet  = "set"  // 写回可读写法：value=<整数> / enabled=<bool>（亦接受 JSON 对象）
	CmdDump = "dump" // 触发一次状态读取（状态本身始终实时可读）
	CmdNoop = "noop" // 空操作：只验证命令链路可达
)

// 事件类型（capability-model.md §4：事件类型属于 Capability 命名空间，
// 不维护平台级写死枚举；本包的事件在 Capabilities() 中声明）。
const (
	EventBooted   = "device-booted"
	EventProbed   = "probed"
	EventSetpoint = "setpoint-changed"
	EventToggled  = "toggled"
)

// set 命令可写的键（与 setpoint@1 / toggle@1 的 action inputSchema 一致）。
const (
	KeyLevel   = "value"
	KeyEnabled = "enabled"
)

// extraTickInterval 是 extra 中调整心跳周期的键（秒）。无硬件设备的心跳完全
// 由本进程产生，默认 10s；允许配置以便演示不同的实时刷新节奏。
const extraTickInterval = "tick_interval_s"

const (
	defaultTickInterval = 10 * time.Second
	minTickInterval     = time.Second
	maxTickInterval     = time.Hour
)

func init() { driverkit.Register(&Adapter{}) }

// Adapter 实现 driverkit.Adapter（以及 DescriptorProvider / CapabilityProvider）。
type Adapter struct{}

// Name 返回适配器名 "demo"。
func (a *Adapter) Name() string { return Name }

// SupportedCommands 返回命令白名单（唯一事实源）。
func (a *Adapter) SupportedCommands() []string { return []string{CmdPing, CmdSet, CmdDump, CmdNoop} }

// PortRequired 声明本适配器**不需要真实端口**。
//
// edge 的配置校验以结构性接口方式读取该方法（examples/* 不得 import internal/*，
// 因此约定写在两侧文档里）：未实现该方法的适配器（如 stcb）默认 port 必填，
// 校验强度不被削弱。
func (a *Adapter) PortRequired() bool { return false }

// Open 立即成功：无硬件可打开。返回的设备由本进程内的真实状态驱动，
// 并启动心跳计数循环（ctx 取消或 Close 后停止）。
func (a *Adapter) Open(ctx context.Context, cfg driverkit.Config, onEvent func(driverkit.Event)) (driverkit.Device, error) {
	if strings.TrimSpace(cfg.ID) == "" {
		return nil, fmt.Errorf("demo: 设备 id 必填")
	}
	// cfg.Port 被有意忽略：参考设备不使用任何端口（README 明确说明）。
	now := time.Now()
	d := &dev{
		id:           cfg.ID,
		openedAt:     now,
		updatedAt:    now,
		tickInterval: tickInterval(cfg.Extra),
		onEvent:      onEvent,
		done:         make(chan struct{}),
	}
	go d.tickLoop(ctx)
	d.emit(EventBooted)
	return d, nil
}

// tickInterval 解析 extra.tick_interval_s 并夹紧到 [1s, 1h]；缺省 10s。
func tickInterval(extra map[string]string) time.Duration {
	raw := strings.TrimSpace(extra[extraTickInterval])
	if raw == "" {
		return defaultTickInterval
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return defaultTickInterval
	}
	d := time.Duration(secs) * time.Second
	if d < minTickInterval {
		return minTickInterval
	}
	if d > maxTickInterval {
		return maxTickInterval
	}
	return d
}

// dev 是一台参考演示设备：全部状态都在本进程内真实维护。
type dev struct {
	id           string
	openedAt     time.Time
	tickInterval time.Duration
	onEvent      func(driverkit.Event)

	mu        sync.Mutex
	ticks     uint64    // 心跳计数（tickLoop 真实递增）
	commands  uint64    // 已接受命令计数
	pings     uint64    // ping 命令计数
	level     int64     // 可写回的数值设定
	enabled   bool      // 可写回的开关
	updatedAt time.Time // 最近一次状态真实变化时间
	closed    bool
	closedAt  time.Time

	done     chan struct{}
	doneOnce sync.Once
}

// ID 返回设备 ID（edge 会另外绑定稳定的 "<edge_id>/<device_id>" 键）。
func (d *dev) ID() string { return d.id }

// Done 在设备关闭时闭合。参考设备不会自发致命错误（无端口可拔），
// 因此本通道只在 Close 后触发。
func (d *dev) Done() <-chan struct{} { return d.done }

// Close 停止心跳循环并标记关闭。幂等。
func (d *dev) Close() error {
	d.doneOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		d.closedAt = time.Now()
		d.mu.Unlock()
		close(d.done)
	})
	return nil
}

// tickLoop 产生真实心跳：每 tickInterval 递增计数并推进 updatedAt。
func (d *dev) tickLoop(ctx context.Context) {
	t := time.NewTicker(d.tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.done:
			return
		case <-t.C:
			d.mu.Lock()
			if d.closed {
				d.mu.Unlock()
				return
			}
			d.ticks++
			d.updatedAt = time.Now()
			d.mu.Unlock()
		}
	}
}

// values 是一次读到的本进程真实状态（Snapshot / Descriptor / 命令摘要共用，
// 保证三者看到的永远是同一份事实）。
type values struct {
	ticks    uint64
	commands uint64
	pings    uint64
	level    int64
	enabled  bool
	uptimeS  int64
	closed   bool
}

// read 原子读取当前状态。uptime 在关闭后冻结在关闭时刻。
func (d *dev) read() values {
	d.mu.Lock()
	v := values{
		ticks: d.ticks, commands: d.commands, pings: d.pings,
		level: d.level, enabled: d.enabled, closed: d.closed,
	}
	closedAt := d.closedAt
	d.mu.Unlock()
	end := time.Now()
	if v.closed && !closedAt.IsZero() {
		end = closedAt
	}
	v.uptimeS = int64(end.Sub(d.openedAt).Seconds())
	return v
}

// Snapshot 返回本进程内的真实状态。不含随机数、不含伪造硬件字段。
func (d *dev) Snapshot() driverkit.State {
	v := d.read()
	d.mu.Lock()
	updatedAt := d.updatedAt
	d.mu.Unlock()
	raw := map[string]any{
		"kind":      Kind,   // 诚实标注：参考演示设备
		"hardware":  "none", // 诚实标注：无硬件
		"uptime_s":  v.uptimeS,
		"ticks":     v.ticks,
		"commands":  v.commands,
		"pings":     v.pings,
		"level":     v.level,
		"enabled":   v.enabled,
		"tick_rate": d.tickInterval.String(),
	}
	return driverkit.State{Online: !v.closed, Raw: raw, UpdatedAt: updatedAt}
}

// Send 执行白名单内命令，真实改变本进程状态并产生事件。
func (d *dev) Send(ctx context.Context, c driverkit.Command) error {
	_, err := d.SendWithResult(ctx, c)
	return err
}

// SendWithResult 实现 edge 侧的 ResultSender 结构性约定（见 internal/edge/edge.go）：
// 执行命令并返回真实执行结果的一行非敏感摘要，使成功命令的 command_ack 也带有
// 可读 detail（而不是只有 status=ok）。摘要全部取自本进程真实状态，不含明文
// secret、访问令牌、本机绝对路径，也不含任何进程 stdout/stderr 原文。
func (d *dev) SendWithResult(ctx context.Context, c driverkit.Command) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := d.alive(); err != nil {
		return "", err
	}
	switch c.Cmd {
	case CmdPing:
		d.touch()
		d.mu.Lock()
		d.pings++
		d.mu.Unlock()
		d.emit(EventProbed)
		v := d.read()
		return fmt.Sprintf("ping pings=%d commands=%d uptime_s=%d", v.pings, v.commands, v.uptimeS), nil
	case CmdDump:
		// 状态始终实时可读：dump 计入命令数并把当前真实状态回给调用方。
		d.touch()
		v := d.read()
		return fmt.Sprintf("dump %s level=%d enabled=%t ticks=%d commands=%d uptime_s=%d",
			Kind, v.level, v.enabled, v.ticks, v.commands, v.uptimeS), nil
	case CmdNoop:
		d.touch()
		v := d.read()
		return fmt.Sprintf("noop commands=%d uptime_s=%d", v.commands, v.uptimeS), nil
	case CmdSet:
		return d.applySet(c.Args)
	default:
		return "", fmt.Errorf("demo: 不支持的命令 %q（白名单: %v）", c.Cmd, (&Adapter{}).SupportedCommands())
	}
}

// alive 报告设备是否仍可接受命令（关闭后 fail-closed，不静默成功）。
func (d *dev) alive() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("demo: %s 已关闭", d.id)
	}
	return nil
}

// touch 记录一次已接受命令并推进 updatedAt。
func (d *dev) touch() {
	d.mu.Lock()
	d.commands++
	d.updatedAt = time.Now()
	d.mu.Unlock()
}

// applySet 解析并写回 value / enabled，只有真实变化才产生事件；
// 返回写回后的真实值摘要（可读回验证，不是回显入参）。
func (d *dev) applySet(args string) (string, error) {
	vals, err := ParseSetArgs(args)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return "", fmt.Errorf("demo: %s 已关闭", d.id)
	}
	var levelChanged, toggleChanged bool
	if raw, ok := vals[KeyLevel]; ok {
		next, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			d.mu.Unlock()
			return "", fmt.Errorf("demo: %s 须为整数，got %q", KeyLevel, raw)
		}
		if next != d.level {
			d.level = next
			levelChanged = true
		}
	}
	if raw, ok := vals[KeyEnabled]; ok {
		next, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			d.mu.Unlock()
			return "", fmt.Errorf("demo: %s 须为布尔值，got %q", KeyEnabled, raw)
		}
		if next != d.enabled {
			d.enabled = next
			toggleChanged = true
		}
	}
	d.commands++
	d.updatedAt = time.Now()
	d.mu.Unlock()

	if levelChanged {
		d.emit(EventSetpoint)
	}
	if toggleChanged {
		d.emit(EventToggled)
	}
	v := d.read()
	return fmt.Sprintf("set %s=%d %s=%t commands=%d", KeyLevel, v.level, KeyEnabled, v.enabled, v.commands), nil
}

// ParseSetArgs 解析 set 命令参数，支持三种等价写法：
//
//   - 键值对：`value=42 enabled=true`（分隔符可为空格、逗号或分号）
//   - JSON 对象：`{"value":42,"enabled":true}`（前端按 action inputSchema 生成的种子即此形态）
//   - 裸整数：`42`（等价于 value=42）
//
// 未知键被拒绝（fail-closed），避免把写不进去的参数静默当成功。
func ParseSetArgs(args string) (map[string]string, error) {
	raw := strings.TrimSpace(args)
	if raw == "" {
		return nil, fmt.Errorf("demo: %s 命令需要参数（%s=<整数> 或 %s=<true|false>）", CmdSet, KeyLevel, KeyEnabled)
	}
	out := map[string]string{}
	if strings.HasPrefix(raw, "{") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return nil, fmt.Errorf("demo: %s 参数不是合法 JSON 对象: %w", CmdSet, err)
		}
		for k, v := range obj {
			s, err := scalarString(v)
			if err != nil {
				return nil, fmt.Errorf("demo: %s 参数 %q: %w", CmdSet, k, err)
			}
			out[k] = s
		}
	} else if !strings.ContainsAny(raw, "=") {
		out[KeyLevel] = raw // 裸值 = 设定值
	} else {
		for _, tok := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' || r == ';'
		}) {
			k, v, ok := strings.Cut(strings.TrimSpace(tok), "=")
			if !ok || strings.TrimSpace(k) == "" {
				return nil, fmt.Errorf("demo: %s 参数 %q 须为 key=value", CmdSet, tok)
			}
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	for k := range out {
		if k != KeyLevel && k != KeyEnabled {
			return nil, fmt.Errorf("demo: %s 不支持的键 %q（可写键: %s, %s）", CmdSet, k, KeyLevel, KeyEnabled)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("demo: %s 命令没有解析出任何可写键", CmdSet)
	}
	return out, nil
}

// scalarString 把 JSON 标量转成字符串（对象/数组拒绝：set 只接受标量）。
func scalarString(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64), nil
	case nil:
		return "", fmt.Errorf("值为 null")
	default:
		return "", fmt.Errorf("值不是标量（%T）", v)
	}
}

// emit 回调设备事件（driverkit 要求适配器保证并发安全调用）。
func (d *dev) emit(typ string) {
	if d.onEvent == nil {
		return
	}
	d.onEvent(driverkit.Event{Type: typ, At: time.Now()})
}
