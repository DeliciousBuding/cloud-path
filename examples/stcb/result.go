package stcb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
)

// 命令后等待真实回帧的窗口。固件回包 ~100ms，窗口留足余量但不拖长 ack
// （edge 的命令超时是 15s，这里最多 ~1.5s）。
const (
	dumpResultWindow   = 800 * time.Millisecond
	syncResultWindow   = 1200 * time.Millisecond
	frameSettleDelay   = 300 * time.Millisecond
	frameRequestDelay  = 250 * time.Millisecond
	frameWaitPollDelay = 20 * time.Millisecond
)

// SendWithResult 实现 edge 侧的 ResultSender 结构性约定（见 internal/edge/edge.go）：
// 执行命令并返回**真实执行结果**的一行非敏感摘要，使成功命令的 command_ack
// 也带有可读 detail，而不是只有 status=ok（本轮验收硬项「执行结果反馈」）。
//
// 红线：摘要只含协议帧与解析后的设备语义字段；不含明文 secret、访问令牌、
// 本机绝对路径（端口名 COM3 属公开的 DeviceMeta.Port，不在此列），
// 也不回传任何 stdout/stderr 原文。edge 出网前还会再过一次 SanitizeDetail。
func (d *dev) SendWithResult(ctx context.Context, c driverkit.Command) (string, error) {
	before := d.dumpAt()
	if err := d.Send(ctx, c); err != nil {
		return "", err
	}
	switch c.Cmd {
	case "dump":
		// dump 自己已经写了 "S"：只等回帧，不重复触发。
		if d.waitDumpAfter(before, dumpResultWindow, ctx) {
			return d.frameSummary("dump"), nil
		}
		return d.staleFrameSummary("dump 已下发，" + fmtWindow(dumpResultWindow) + "内未收到回帧"), nil
	case "sync":
		hhmm := c.Args
		if hhmm == "" {
			hhmm = BeijingNow().Format("1504")
		}
		// 对时的真实结果只能由**新回帧**证明：主动取一帧，只有收到比执行前更新的
		// 帧才报同步后的板钟与漂移，否则诚实说明未回帧（绝不拿旧帧冒充同步结果）。
		if d.requestFrame(ctx, before, frameSettleDelay, syncResultWindow) {
			if s, ok := d.syncSummary(); ok {
				return "synced " + s, nil
			}
		}
		return fmt.Sprintf("synced hhmm=%s（%s内未收到回帧）", hhmm, fmtWindow(syncResultWindow)), nil
	case "trigger", "open":
		d.requestFrame(ctx, before, frameRequestDelay, dumpResultWindow)
		if d.hasFreshDump(before) {
			return d.frameSummary(c.Cmd + "(" + wireByte(c.Cmd) + ")"), nil
		}
		return fmt.Sprintf("%s(%s) 已下发，%s内未收到回帧", c.Cmd, wireByte(c.Cmd), fmtWindow(dumpResultWindow)), nil
	case "isp":
		// ISP 会让固件停止回应：等回帧没有意义，直接给出诚实摘要。
		return "isp(D) 已进入 ISP 下载模式，设备停止回应，需重新上电", nil
	case "raw":
		d.requestFrame(ctx, before, frameRequestDelay, dumpResultWindow)
		if d.hasFreshDump(before) {
			return d.frameSummary("raw(" + c.Args + ")"), nil
		}
		return fmt.Sprintf("raw(%s) 已下发，%s内未收到回帧", c.Args, fmtWindow(dumpResultWindow)), nil
	default:
		return "cmd=" + c.Cmd, nil
	}
}

// wireByte 返回命令对应的固件单字节命令（仅用于摘要可读性）。
func wireByte(cmd string) string {
	switch cmd {
	case "dump":
		return "S"
	case "trigger":
		return "R"
	case "open":
		return "O"
	case "isp":
		return "D"
	default:
		return "?"
	}
}

func fmtWindow(w time.Duration) string { return w.String() }

// dumpAt 返回最近一帧转储的解析时刻（零值表示从未收到）。
func (d *dev) dumpAt() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastDump
}

// hasFreshDump 报告是否已收到比 before 更新的转储帧。
func (d *dev) hasFreshDump(before time.Time) bool {
	last := d.dumpAt()
	return !last.IsZero() && last.After(before)
}

// waitDumpAfter 在 window 内轮询等待一帧比 before 更新的转储。
// ctx 取消或端口死亡立即返回 false（不阻塞命令超时预算）。
func (d *dev) waitDumpAfter(before time.Time, window time.Duration, ctx context.Context) bool {
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-d.done:
			return false
		default:
		}
		if d.hasFreshDump(before) {
			return true
		}
		time.Sleep(frameWaitPollDelay)
	}
	return d.hasFreshDump(before)
}

// requestFrame 先等固件落定，再主动请求一帧转储并等待回包。
// 用于 trigger/open/raw/sync：这些命令本身不会让固件主动吐状态。
func (d *dev) requestFrame(ctx context.Context, before time.Time, settle, window time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-d.done:
		return false
	case <-time.After(settle):
	}
	if err := d.write([]byte("S")); err != nil {
		return false
	}
	return d.waitDumpAfter(before, window, ctx)
}

// frameSummary 用当前真实转储帧组装一行摘要（prefix 标明是哪条命令的结果）。
func (d *dev) frameSummary(prefix string) string {
	d.mu.Lock()
	dump := d.dump
	d.mu.Unlock()
	if dump == nil {
		return prefix + " 已下发（暂无转储帧）"
	}
	slots := make([]string, 0, len(dump.Slots))
	for _, s := range dump.Slots {
		slots = append(slots, SlotLabel(s))
	}
	return fmt.Sprintf("%s dump_raw=%s clock=%02d:%02d state=%s slots=%s drift_min=%g",
		prefix, dump.Raw, dump.Hour, dump.Min, StateLabel(dump.State),
		strings.Join(slots, "/"), DriftMin(dump.Hour, dump.Min, BeijingNow()))
}

// staleFrameSummary 在未收到新帧时诚实报告：说明未回帧，并附最近已知帧与其年龄。
func (d *dev) staleFrameSummary(prefix string) string {
	d.mu.Lock()
	dump, last := d.dump, d.lastDump
	d.mu.Unlock()
	if dump == nil || last.IsZero() {
		return prefix + "（尚无历史帧）"
	}
	return fmt.Sprintf("%s；last clock=%02d:%02d state=%s age=%s",
		prefix, dump.Hour, dump.Min, StateLabel(dump.State),
		time.Since(last).Round(time.Second).String())
}

// syncSummary 返回对时后的真实板钟与漂移（只有在收到新帧时才成立）。
func (d *dev) syncSummary() (string, bool) {
	d.mu.Lock()
	dump := d.dump
	d.mu.Unlock()
	if dump == nil {
		return "", false
	}
	return fmt.Sprintf("clock=%02d:%02d drift_min=%g state=%s",
		dump.Hour, dump.Min, DriftMin(dump.Hour, dump.Min, BeijingNow()), StateLabel(dump.State)), true
}
