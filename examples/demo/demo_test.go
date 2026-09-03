package demo

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// 契约编译证明：Adapter 同时满足 driverkit 的四个接口（工厂 + 静态 Descriptor +
// Capability catalog + 设备级实时 Descriptor）。任何签名漂移都在这里编译失败。
var (
	_ driverkit.Adapter            = (*Adapter)(nil)
	_ driverkit.DescriptorProvider = (*Adapter)(nil)
	_ driverkit.CapabilityProvider = (*Adapter)(nil)
	_ driverkit.DescriptorSource   = (*dev)(nil)
)

// eventRecorder 收集适配器回调的事件（driverkit 要求回调并发安全）。
type eventRecorder struct {
	mu sync.Mutex
	ev []driverkit.Event
}

func (r *eventRecorder) fn(ev driverkit.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ev = append(r.ev, ev)
}

func (r *eventRecorder) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.ev))
	for _, e := range r.ev {
		out = append(out, e.Type)
	}
	return out
}

func (r *eventRecorder) count(typ string) int {
	n := 0
	for _, t := range r.types() {
		if t == typ {
			n++
		}
	}
	return n
}

// openDev 打开一台参考设备（无端口、无硬件），返回设备与事件记录器。
func openDev(t *testing.T, id string, extra map[string]string) (driverkit.Device, *eventRecorder) {
	t.Helper()
	rec := &eventRecorder{}
	dev, err := (&Adapter{}).Open(context.Background(), driverkit.Config{ID: id, Extra: extra}, rec.fn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = dev.Close() })
	return dev, rec
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("条件在超时内未满足")
}

func num(t *testing.T, raw map[string]any, key string) int64 {
	t.Helper()
	switch v := raw[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case uint64:
		return int64(v)
	case float64:
		return int64(v)
	default:
		t.Fatalf("raw[%q] 不是数值: %T(%v)", key, v, v)
		return 0
	}
}

func TestAdapterNameAndCommandWhitelist(t *testing.T) {
	a := &Adapter{}
	if a.Name() != "demo" {
		t.Fatalf("Name() = %q, want %q", a.Name(), "demo")
	}
	want := []string{CmdPing, CmdSet, CmdDump, CmdNoop}
	got := a.SupportedCommands()
	if len(got) != len(want) {
		t.Fatalf("SupportedCommands() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SupportedCommands() = %v, want %v", got, want)
		}
	}
	// 注册表里必须能按名取到（init() 已注册）。
	reg, ok := driverkit.Get("demo")
	if !ok {
		t.Fatal("driverkit 注册表缺少 demo 适配器")
	}
	if reg.Name() != "demo" {
		t.Fatalf("注册表返回 %q", reg.Name())
	}
}

// TestPortNotRequired 锁定无硬件放宽约定：demo 声明 port 非必填，
// 且 Open 在 port 为空时立即成功（这是「同学没有板子也能接入」的前提）。
func TestPortNotRequired(t *testing.T) {
	if (&Adapter{}).PortRequired() {
		t.Fatal("demo 适配器不得要求真实端口")
	}
	dev, _ := openDev(t, "d1", nil)
	st := dev.Snapshot()
	if !st.Online {
		t.Fatal("无硬件设备 Open 后应立即在线")
	}
}

func TestOpenRejectsEmptyID(t *testing.T) {
	if _, err := (&Adapter{}).Open(context.Background(), driverkit.Config{}, nil); err == nil {
		t.Fatal("空 id 应被拒绝")
	}
}

// TestSnapshotIsRealInProcessState 验证上报的是本进程真实状态：
// uptime 单调增长、心跳计数由内部循环真实递增、初值为 0（不是随机假数）。
func TestSnapshotIsRealInProcessState(t *testing.T) {
	dev, _ := openDev(t, "d1", map[string]string{extraTickInterval: "1"})
	first := dev.Snapshot()
	if num(t, first.Raw, "uptime_s") != 0 {
		t.Fatalf("刚打开的设备 uptime 应为 0，got %v", first.Raw["uptime_s"])
	}
	if num(t, first.Raw, "commands") != 0 || num(t, first.Raw, "pings") != 0 {
		t.Fatalf("命令计数初值应为 0: %v", first.Raw)
	}
	waitFor(t, 3*time.Second, func() bool {
		st := dev.Snapshot()
		return num(t, st.Raw, "ticks") >= 1 && num(t, st.Raw, "uptime_s") >= 1
	})
	// 单调性：后一拍不得回退。
	a := dev.Snapshot()
	time.Sleep(1100 * time.Millisecond)
	b := dev.Snapshot()
	if num(t, b.Raw, "ticks") <= num(t, a.Raw, "ticks") {
		t.Fatalf("心跳计数未递增: %v -> %v", a.Raw["ticks"], b.Raw["ticks"])
	}
	if num(t, b.Raw, "uptime_s") < num(t, a.Raw, "uptime_s") {
		t.Fatalf("uptime 回退: %v -> %v", a.Raw["uptime_s"], b.Raw["uptime_s"])
	}
}

// TestSendMutatesRealState 验证 set 真实改变进程内状态且可原样读回（三种写法）。
func TestSendMutatesRealState(t *testing.T) {
	dev, _ := openDev(t, "d1", nil)
	ctx := context.Background()

	if err := dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: "value=42"}); err != nil {
		t.Fatalf("set value=42: %v", err)
	}
	if got := num(t, dev.Snapshot().Raw, "level"); got != 42 {
		t.Fatalf("set 后 level = %d, want 42", got)
	}
	if err := dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: `{"value":7,"enabled":true}`}); err != nil {
		t.Fatalf("set json: %v", err)
	}
	st := dev.Snapshot()
	if num(t, st.Raw, "level") != 7 {
		t.Fatalf("JSON 写法后 level = %v, want 7", st.Raw["level"])
	}
	if v, ok := st.Raw["enabled"].(bool); !ok || !v {
		t.Fatalf("JSON 写法后 enabled = %v, want true", st.Raw["enabled"])
	}
	if err := dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: "3"}); err != nil {
		t.Fatalf("set 裸值: %v", err)
	}
	if got := num(t, dev.Snapshot().Raw, "level"); got != 3 {
		t.Fatalf("裸值写法后 level = %d, want 3", got)
	}
	if err := dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: "enabled=false"}); err != nil {
		t.Fatalf("set enabled=false: %v", err)
	}
	if v := dev.Snapshot().Raw["enabled"].(bool); v {
		t.Fatal("enabled 应为 false")
	}
	// 命令计数真实累计：4 条 set。
	if got := num(t, dev.Snapshot().Raw, "commands"); got != 4 {
		t.Fatalf("commands = %d, want 4", got)
	}
}

func TestPingAndDumpAndNoop(t *testing.T) {
	dev, rec := openDev(t, "d1", nil)
	ctx := context.Background()
	for _, c := range []string{CmdPing, CmdPing, CmdDump, CmdNoop} {
		if err := dev.Send(ctx, driverkit.Command{Cmd: c}); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
	}
	st := dev.Snapshot()
	if num(t, st.Raw, "pings") != 2 {
		t.Fatalf("pings = %v, want 2", st.Raw["pings"])
	}
	if num(t, st.Raw, "commands") != 4 {
		t.Fatalf("commands = %v, want 4", st.Raw["commands"])
	}
	// dump/noop 不得制造事件（否则每拍轮询都会刷屏事件流）。
	if rec.count(EventProbed) != 2 {
		t.Fatalf("probed 事件数 = %d, want 2", rec.count(EventProbed))
	}
	for _, typ := range rec.types() {
		if typ != EventBooted && typ != EventProbed {
			t.Fatalf("dump/noop 不应产生事件 %q", typ)
		}
	}
}

func TestSetRejectsBadArgs(t *testing.T) {
	dev, _ := openDev(t, "d1", nil)
	ctx := context.Background()
	bad := []string{
		"",                  // 空参数
		"nope=1",            // 未知键
		"value=abc",         // 非整数
		"value=1.5",         // 非整数
		"enabled=maybe",     // 非布尔
		"value",             // 缺 =
		`{"value":{"a":1}}`, // 非标量
		`{"level":1}`,       // 未知键（JSON 形态）
	}
	for _, args := range bad {
		before := num(t, dev.Snapshot().Raw, "level")
		err := dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: args})
		if err == nil {
			t.Fatalf("set %q 应被拒绝", args)
		}
		if after := num(t, dev.Snapshot().Raw, "level"); after != before {
			t.Fatalf("set %q 被拒绝却改了状态: %d -> %d", args, before, after)
		}
	}
}

// TestUnsupportedCommandFailsClosed 验证白名单外的命令返回错误：
// edge 据此回 failed ack，绝不静默成功。
func TestUnsupportedCommandFailsClosed(t *testing.T) {
	dev, _ := openDev(t, "d1", nil)
	err := dev.Send(context.Background(), driverkit.Command{Cmd: "sync"})
	if err == nil {
		t.Fatal("stcb 专属命令 sync 在 demo 上必须被拒绝（核心不得假设设备语义）")
	}
	if !strings.Contains(err.Error(), "ping") {
		t.Fatalf("错误应带白名单: %v", err)
	}
	if num(t, dev.Snapshot().Raw, "commands") != 0 {
		t.Fatal("被拒绝的命令不得计入已接受命令数")
	}
}

func TestEventsOnRealChangeOnly(t *testing.T) {
	dev, rec := openDev(t, "d1", nil)
	ctx := context.Background()
	if rec.count(EventBooted) != 1 {
		t.Fatalf("Open 应产生一次 %s，got %v", EventBooted, rec.types())
	}
	// 写入同值不产生事件（只有真实变化才是事件）。
	_ = dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: "value=0"})
	if rec.count(EventSetpoint) != 0 {
		t.Fatalf("同值写入不应产生事件: %v", rec.types())
	}
	_ = dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: "value=1"})
	if rec.count(EventSetpoint) != 1 {
		t.Fatalf("value 变化应产生 %s: %v", EventSetpoint, rec.types())
	}
	_ = dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: "enabled=true"})
	if rec.count(EventToggled) != 1 {
		t.Fatalf("enabled 变化应产生 %s: %v", EventToggled, rec.types())
	}
	_ = dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: "enabled=true"})
	if rec.count(EventToggled) != 1 {
		t.Fatalf("同值写入不应重复产生事件: %v", rec.types())
	}
}

// TestNoRandomness 反向验证「不是随机假数」：两台设备跑同一命令序列后状态完全一致。
func TestNoRandomness(t *testing.T) {
	seq := []driverkit.Command{
		{Cmd: CmdSet, Args: "value=11"},
		{Cmd: CmdPing},
		{Cmd: CmdSet, Args: "enabled=true"},
		{Cmd: CmdDump},
		{Cmd: CmdNoop},
	}
	var levels, enableds, commands []any
	for i := 0; i < 2; i++ {
		dev, _ := openDev(t, fmt.Sprintf("d%d", i), nil)
		for _, c := range seq {
			if err := dev.Send(context.Background(), c); err != nil {
				t.Fatalf("%s: %v", c.Cmd, err)
			}
		}
		raw := dev.Snapshot().Raw
		levels = append(levels, raw["level"])
		enableds = append(enableds, raw["enabled"])
		commands = append(commands, raw["commands"])
	}
	if levels[0] != levels[1] || enableds[0] != enableds[1] || commands[0] != commands[1] {
		t.Fatalf("相同命令序列产生不同状态（疑似随机假数）: %v %v %v", levels, enableds, commands)
	}
}

func TestDescriptorContract(t *testing.T) {
	a := &Adapter{}
	static := a.Descriptor(driverkit.Config{ID: "d1"})
	if err := static.Validate(); err != nil {
		t.Fatalf("静态 Descriptor 校验失败: %v", err)
	}
	if len(static.Entities) != 6 {
		t.Fatalf("Entity 数 = %d, want 6", len(static.Entities))
	}
	if static.Status != model.DeviceUnavailable {
		t.Fatalf("未打开的静态 Descriptor status = %q, want unavailable", static.Status)
	}

	dev, _ := openDev(t, "d1", nil)
	live := dev.(driverkit.DescriptorSource).Descriptor()
	if err := live.Validate(); err != nil {
		t.Fatalf("实时 Descriptor 校验失败: %v", err)
	}
	if live.Status != model.DeviceOnline {
		t.Fatalf("打开后 status = %q, want online", live.Status)
	}
	// 每个 Entity 都必须有主观测，否则 schema-driven UI 只能回落成空面板。
	for _, e := range live.Entities {
		if len(e.Observations) == 0 {
			t.Errorf("entity %q 没有观测值", e.EntityID)
		}
		for k, o := range e.Observations {
			if err := o.Validate(); err != nil {
				t.Errorf("entity %q observation %q 校验失败: %v", e.EntityID, k, err)
			}
			if !o.ObservedAt.IsZero() || !o.ReceivedAt.IsZero() {
				t.Errorf("entity %q observation %q 不得携带时间戳（会击穿 edge 的 Descriptor diff 抑制）", e.EntityID, k)
			}
		}
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := dev.(driverkit.DescriptorSource).Descriptor().Status; got != model.DeviceOffline {
		t.Fatalf("关闭后 status = %q, want offline", got)
	}
}

// TestCapabilityContract 验证 catalog 合法，且**每条白名单命令都能在 UI 里成为
// 真实控件**（capability action key == 命令名），而不是回落成 JSON 编辑器。
func TestCapabilityContract(t *testing.T) {
	a := &Adapter{}
	caps := a.Capabilities()
	if len(caps) != 5 {
		t.Fatalf("Capability 数 = %d, want 5", len(caps))
	}
	actions := map[string]bool{}
	seen := map[string]bool{}
	for _, c := range caps {
		if err := c.Validate(); err != nil {
			t.Fatalf("capability %q 校验失败: %v", c.Metadata.ID, err)
		}
		if seen[c.Metadata.ID] {
			t.Fatalf("capability ID 重复: %q", c.Metadata.ID)
		}
		seen[c.Metadata.ID] = true
		for name := range c.Spec.Actions {
			actions[name] = true
		}
	}
	for _, cmd := range a.SupportedCommands() {
		if !actions[cmd] {
			t.Errorf("命令 %q 未在任何 Capability 的 actions 中声明，前端只能回落到白名单文本按钮", cmd)
		}
	}
	for name := range actions {
		if !contains(a.SupportedCommands(), name) {
			t.Errorf("Capability action %q 不在命令白名单内（会被 server 拒绝，属虚假控件）", name)
		}
	}
	// Descriptor 引用的 capability 必须都在 catalog 里（否则 UI 解析不到 presentation）。
	catalog := map[string]bool{}
	for _, c := range caps {
		catalog[c.Metadata.ID] = true
	}
	for _, e := range a.Descriptor(driverkit.Config{ID: "d1"}).Entities {
		for _, ref := range e.Capabilities {
			if !catalog[ref] {
				t.Errorf("entity %q 引用了 catalog 未提供的 capability %q", e.EntityID, ref)
			}
		}
	}
}

// TestHonestLabeling 是诚实性红线的反向测试：任何一处把 demo 标成真实硬件
// （或漏标类别）都必须失败。
func TestHonestLabeling(t *testing.T) {
	dev, _ := openDev(t, "d1", nil)
	raw := dev.Snapshot().Raw
	if raw["kind"] != Kind {
		t.Fatalf("State.Raw.kind = %v, want %q", raw["kind"], Kind)
	}
	if raw["hardware"] != "none" {
		t.Fatalf("State.Raw.hardware = %v, want none", raw["hardware"])
	}
	desc := dev.(driverkit.DescriptorSource).Descriptor()
	if !strings.Contains(desc.Model, "Reference Demo Device") || !strings.Contains(desc.Model, "no hardware") {
		t.Fatalf("Descriptor.Model 必须明示无硬件参考设备，got %q", desc.Model)
	}
	var status string
	for _, e := range desc.Entities {
		if e.EntityID != entityDiagnostics {
			continue
		}
		status = fmt.Sprint(e.Observations["status"].Value)
	}
	if status != Kind {
		t.Fatalf("诊断观测值应为 %q（UI 卡片 headline），got %q", Kind, status)
	}
	// 文档侧同样不得伪装：plugin.yaml 的 hardware 权限必须为空，README 必须声明非真实硬件。
	manifest, err := os.ReadFile("plugin.yaml")
	if err != nil {
		t.Fatalf("read plugin.yaml: %v", err)
	}
	if !strings.Contains(string(manifest), "hardware: []") {
		t.Fatal("plugin.yaml 的 permissions.hardware 必须为空（无硬件）")
	}
	if !strings.Contains(string(manifest), "reference demo device") {
		t.Fatal("plugin.yaml 必须标注 reference demo device")
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(readme), "不是真实硬件") {
		t.Fatal("README.md 必须明确声明这不是真实硬件")
	}
}

func TestCloseIsIdempotentAndFailsClosed(t *testing.T) {
	dev, _ := openDev(t, "d1", map[string]string{extraTickInterval: "1"})
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close 应幂等: %v", err)
	}
	select {
	case <-dev.Done():
	default:
		t.Fatal("Close 后 Done() 必须已闭合")
	}
	if dev.Snapshot().Online {
		t.Fatal("Close 后必须离线")
	}
	if err := dev.Send(context.Background(), driverkit.Command{Cmd: CmdPing}); err == nil {
		t.Fatal("Close 后命令必须失败（不得静默成功）")
	}
	ticks := num(t, dev.Snapshot().Raw, "ticks")
	time.Sleep(1200 * time.Millisecond)
	if num(t, dev.Snapshot().Raw, "ticks") != ticks {
		t.Fatal("Close 后心跳循环必须停止")
	}
}

// TestConcurrentSafe 验证 Snapshot / Send / Descriptor / Close 并发安全。
func TestConcurrentSafe(t *testing.T) {
	dev, _ := openDev(t, "d1", map[string]string{extraTickInterval: "1"})
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				_ = dev.Snapshot()
				_ = dev.(driverkit.DescriptorSource).Descriptor()
				_ = dev.Send(ctx, driverkit.Command{Cmd: CmdSet, Args: fmt.Sprintf("value=%d", i*100+j)})
				_ = dev.Send(ctx, driverkit.Command{Cmd: CmdPing})
			}
		}(i)
	}
	wg.Wait()
	if got := num(t, dev.Snapshot().Raw, "commands"); got != 8*25*2 {
		t.Fatalf("并发命令计数丢失: %d, want %d", got, 8*25*2)
	}
}

func TestTickIntervalClamped(t *testing.T) {
	for _, extra := range []map[string]string{
		{extraTickInterval: "0"},
		{extraTickInterval: "-5"},
		{extraTickInterval: "abc"},
		nil,
	} {
		if got := tickInterval(extra); got != defaultTickInterval {
			t.Fatalf("tickInterval(%v) = %v, want %v", extra, got, defaultTickInterval)
		}
	}
	if got := tickInterval(map[string]string{extraTickInterval: "999999"}); got != maxTickInterval {
		t.Fatalf("超大心跳周期未夹紧: %v", got)
	}
	if got := tickInterval(map[string]string{extraTickInterval: "5"}); got != 5*time.Second {
		t.Fatalf("心跳周期 = %v, want 5s", got)
	}
}

// TestNoInternalImports 锁定拆仓红线：examples/demo 不得 import internal/*。
func TestNoInternalImports(t *testing.T) {
	const prefix = "github.com/DeliciousBuding/cloud-path/internal/"
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	dir := filepath.Dir(file)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, prefix) {
				t.Errorf("%s 依赖 internal/*: %s", e.Name(), p)
			}
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
