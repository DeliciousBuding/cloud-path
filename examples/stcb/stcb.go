// Package stcb 实现 STC-B（IAP15F2K61S2）学习板的设备适配器：
// 串口协议解析（转储/事件）+ 命令通道（对时/转储/触发/开盖/ISP）。
// 协议契约见 docs/protocol.md。这是 Cloudpath 的第一个官方 reference device。
package stcb

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
	"go.bug.st/serial"
)

func init() { driverkit.Register(&Adapter{}) }

// Adapter 实现 driverkit.Adapter。
type Adapter struct{}

func (a *Adapter) Name() string { return "stcb" }

func (a *Adapter) SupportedCommands() []string {
	return []string{"sync", "dump", "trigger", "open", "isp", "raw"}
}

// Open 打开串口并启动 RX 循环。拔线/端口错误通过 Device.Done() 通知上层。
func (a *Adapter) Open(ctx context.Context, cfg driverkit.Config, onEvent func(driverkit.Event)) (driverkit.Device, error) {
	baud := cfg.Baud
	if baud <= 0 {
		baud = 9600
	}
	port, err := serial.Open(cfg.Port, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, fmt.Errorf("stcb: open %s: %w", cfg.Port, err)
	}
	if err := port.SetReadTimeout(200 * time.Millisecond); err != nil {
		port.Close()
		return nil, fmt.Errorf("stcb: set timeout: %w", err)
	}
	d := &dev{
		id:       cfg.ID,
		name:     cfg.Name,
		port:     port,
		portName: cfg.Port,
		done:     make(chan struct{}),
		onEvent:  onEvent,
	}
	go d.rxLoop(ctx)
	return d, nil
}

type dev struct {
	id       string
	name     string
	portName string
	port     serial.Port
	onEvent  func(driverkit.Event)

	mu       sync.Mutex
	dump     *Dump
	lastDump time.Time
	dead     bool

	done     chan struct{}
	doneOnce sync.Once
}

func (d *dev) ID() string { return d.id }

// rxLoop 按行读串口：解析转储更新状态、解析事件回调 onEvent。
func (d *dev) rxLoop(ctx context.Context) {
	defer d.markDead()
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 256)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.done:
			return
		default:
		}
		n, err := d.port.Read(tmp)
		if err != nil {
			return // 拔线/端口死 → Done
		}
		if n == 0 {
			continue
		}
		buf = append(buf, tmp[:n]...)
		for {
			i := indexByte(buf, '\n')
			if i < 0 {
				break
			}
			line := string(buf[:i])
			buf = append(buf[:0], buf[i+1:]...)
			d.handleLine(strings.TrimSpace(strings.ReplaceAll(line, "\x00", "")))
		}
		if len(buf) > 200 { // 无 EOL 垃圾防洪
			buf = buf[:0]
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func (d *dev) handleLine(line string) {
	if line == "" {
		return
	}
	if dump, ok := ParseDump(line); ok {
		d.mu.Lock()
		d.dump = &dump
		d.lastDump = time.Now()
		d.mu.Unlock()
		return
	}
	if ev, ok := ParseEvent(line); ok {
		if d.onEvent != nil {
			d.onEvent(driverkit.Event{Type: ev, At: time.Now()})
		}
	}
}

func (d *dev) markDead() {
	d.mu.Lock()
	d.dead = true
	d.mu.Unlock()
	d.doneOnce.Do(func() { close(d.done) })
}

func (d *dev) Done() <-chan struct{} { return d.done }

func (d *dev) Close() error {
	d.doneOnce.Do(func() { close(d.done) })
	return d.port.Close()
}

// Send 执行命令（白名单内）。sync 逐字节慢发：固件命令缓冲仅 1 字节。
func (d *dev) Send(ctx context.Context, c driverkit.Command) error {
	d.mu.Lock()
	dead := d.dead
	d.mu.Unlock()
	if dead {
		return fmt.Errorf("stcb: %s port dead", d.id)
	}
	switch c.Cmd {
	case "dump":
		return d.write([]byte("S"))
	case "trigger":
		return d.write([]byte("R"))
	case "open":
		return d.write([]byte("O"))
	case "isp":
		return d.write([]byte("D"))
	case "raw":
		if c.Args == "" {
			return fmt.Errorf("stcb: raw 命令需要 args")
		}
		return d.write([]byte(c.Args))
	case "sync":
		hhmm := c.Args
		if hhmm == "" {
			hhmm = BeijingNow().Format("1504")
		}
		if !validHHMM(hhmm) {
			return fmt.Errorf("stcb: sync args 须为 4 位 HHMM，got %q", hhmm)
		}
		// 逐字节慢发（固件 UART 命令缓冲仅 1 字节，快发会丢）
		for _, ch := range []byte("T" + hhmm) {
			if err := d.write([]byte{ch}); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-d.done:
				return fmt.Errorf("stcb: port dead during sync")
			case <-time.After(50 * time.Millisecond):
			}
		}
		return nil
	default:
		return fmt.Errorf("stcb: 不支持的命令 %q（白名单: %v）", c.Cmd, d.SupportedCommandsStatic())
	}
}

// SupportedCommandsStatic 供 Send 错误信息复用（避免依赖 Adapter 实例）。
func (d *dev) SupportedCommandsStatic() []string {
	return []string{"sync", "dump", "trigger", "open", "isp", "raw"}
}

func (d *dev) write(b []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dead {
		return fmt.Errorf("stcb: %s port dead", d.id)
	}
	_, err := d.port.Write(b)
	if err != nil {
		go d.markDead()
		return fmt.Errorf("stcb: write: %w", err)
	}
	return nil
}

func validHHMM(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	hh, mm := (s[0]-'0')*10+(s[1]-'0'), (s[2]-'0')*10+(s[3]-'0')
	return hh <= 23 && mm <= 59
}

// Snapshot 输出统一状态：时钟、状态机、三槽、北京时间与漂移。
func (d *dev) Snapshot() driverkit.State {
	now := time.Now()
	bj := BeijingNow()
	d.mu.Lock()
	dump, last, dead := d.dump, d.lastDump, d.dead
	d.mu.Unlock()

	online := !dead && !last.IsZero() && now.Sub(last) < 30*time.Second
	// raw 只放设备语义字段（时钟/状态机/槽位/漂移）——易变字段（北京时间、转储 age）
	// 会击穿 edge 的 diff 抑制；展示侧（前端/server）自行计算
	raw := map[string]any{}
	if dump != nil {
		raw["state"] = dump.State
		raw["state_label"] = StateLabel(dump.State)
		raw["hour"] = dump.Hour
		raw["min"] = dump.Min
		raw["clock"] = fmt.Sprintf("%02d:%02d", dump.Hour, dump.Min)
		slots := make([]map[string]any, 0, len(dump.Slots))
		for i, s := range dump.Slots {
			slots = append(slots, map[string]any{
				"index": i, "code": s, "label": SlotLabel(s),
			})
		}
		raw["slots"] = slots
		raw["drift_min"] = DriftMin(dump.Hour, dump.Min, bj)
		raw["dump_raw"] = dump.Raw
	}
	return driverkit.State{Online: online, Raw: raw, UpdatedAt: last}
}
