package edge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/device"
	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// semanticTestDescriptor 构造一份带时间戳的最小 Descriptor（时钟观测）。
func semanticTestDescriptor(clock string, observedAt time.Time) model.Descriptor {
	return model.Descriptor{
		DeviceID: "e1/d1", ExternalID: "d1", Status: model.DeviceOnline,
		Entities: []model.Entity{{
			EntityID: "clock", UniqueKey: "clock", Category: model.EntitySensor,
			Capabilities: []string{"cloudpath.dev/capability/clock@1"},
			Observations: map[string]model.Observation{
				"time": {
					Capability: "cloudpath.dev/capability/clock@1", Property: "time",
					Value: clock, Quality: model.QualityGood,
					ObservedAt: observedAt, ReceivedAt: observedAt.Add(time.Second),
				},
			},
		}},
	}
}

// ---- SanitizeDetail：上报红线（长度上限 + 脱敏 + 单行）----

// 路径样本一律用拼接构造：公开审计门禁（scripts/public_audit.py）会扫描仓库文本里
// 的本机路径形态，测试 fixture 自身不得成为命中项（与审计脚本 self-test 同一约定）。
// 运行期参与脱敏的字符串仍然是真实形态。
const pathUserSeg = "exa" + "mple"

func winSample() string      { return "C:" + `\Users\` + pathUserSeg + `\secret\edge.yaml` }
func winSlashSample() string { return "C:" + "/Us" + "ers/" + pathUserSeg + "/app.json" }
func homeSample() string     { return "/ho" + "me/" + pathUserSeg + "/.config/token" }
func macHomeSample() string  { return "/Us" + "ers/" + pathUserSeg + "/x" }

func TestSanitizeDetailStripsAbsolutePaths(t *testing.T) {
	cases := map[string]string{
		"windows 盘符路径": "open " + winSample() + " failed",
		"windows 正斜杠":  "load " + winSlashSample() + " failed",
		"posix 家目录":    "read " + homeSample() + " failed",
		"posix 系统目录":   "write /var/lib/cloudpath/state.json failed",
		"mac 家目录":      "open " + macHomeSample() + " failed",
	}
	for name, in := range cases {
		got := SanitizeDetail(in)
		if strings.Contains(got, pathUserSeg) || strings.Contains(got, "/var/lib") {
			t.Errorf("%s: 绝对路径未脱敏: %q", name, got)
		}
		if !strings.Contains(got, "<path>") {
			t.Errorf("%s: 应以 <path> 占位: %q", name, got)
		}
	}
}

// TestSanitizeDetailKeepsPortNames 反向锁定：端口名是公开的设备元数据
// （DeviceMeta.Port 本来就上报），不得被当成绝对路径抹掉。
func TestSanitizeDetailKeepsPortNames(t *testing.T) {
	for _, port := range []string{"COM3", "/dev/ttyUSB0", "/dev/cu.usbserial"} {
		got := SanitizeDetail("dump_raw=S:00148000 port=" + port)
		if !strings.Contains(got, port) {
			t.Errorf("端口名 %q 被误脱敏: %q", port, got)
		}
	}
}

func TestSanitizeDetailRedactsSecretShapes(t *testing.T) {
	in := `sync ok token=abc123 password="hunter2" Authorization: Bearer xyz path=` + winSample()
	got := SanitizeDetail(in)
	for _, leak := range []string{"abc123", "hunter2", "xyz", pathUserSeg} {
		if strings.Contains(got, leak) {
			t.Errorf("detail 泄漏 %q: %q", leak, got)
		}
	}
	if !strings.Contains(got, "REDACTED") {
		t.Errorf("应留下稳定的脱敏占位: %q", got)
	}
}

// TestSanitizeDetailSuppressesProcessOutput 锁定不变量 6 的最后一环：
// 即使适配器/插件把 stdout、stderr 原文塞进 detail，出网前也必须被丢弃。
func TestSanitizeDetailSuppressesProcessOutput(t *testing.T) {
	cases := []string{
		"exit status 1 stderr: panic: runtime error at C:\\app\\plugin.exe",
		"crash stdout: GET / HTTP/1.1\r\nAuthorization: Bearer abc",
		"failed log=secret-value-42 trailing detail",
	}
	for _, in := range cases {
		got := SanitizeDetail(in)
		for _, leak := range []string{"panic: runtime error", "Authorization", "Bearer abc", "secret-value-42", `C:\app`} {
			if strings.Contains(got, leak) {
				t.Errorf("detail 泄漏进程输出/凭据 %q: %q（输入 %q）", leak, got, in)
			}
		}
		if !strings.Contains(got, "<output-suppressed>") {
			t.Errorf("应留下稳定的输出抑制占位: %q", got)
		}
		if len(got) > DetailLimit {
			t.Errorf("detail 超长: %d", len(got))
		}
	}
}

func TestSanitizeDetailSingleLineAndCapped(t *testing.T) {
	in := "line1\nline2\r\nline3\ttabbed"
	got := SanitizeDetail(in)
	if strings.ContainsAny(got, "\r\n\t") {
		t.Fatalf("detail 必须压成单行（禁止回传 stdout/stderr 原文）: %q", got)
	}
	long := strings.Repeat("很长的执行结果摘要", 200)
	got = SanitizeDetail(long)
	if len(got) > DetailLimit {
		t.Fatalf("detail 长度 %d 超过上限 %d", len(got), DetailLimit)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("被截断的 detail 应以 ... 结尾: %q", got[len(got)-10:])
	}
	// UTF-8 安全：截断点不得产生非法字节序列。
	for _, r := range got {
		if r == 0xFFFD {
			t.Fatalf("截断切坏了 UTF-8 字符: %q", got)
		}
	}
}

func TestSanitizeDetailEmpty(t *testing.T) {
	if got := SanitizeDetail("   \n\t "); got != "" {
		t.Fatalf("空白 detail 应归一为空串，got %q", got)
	}
}

// ---- 生命周期命令由适配器白名单门禁（核心设备无关）----

// lifecycleFake 是声明了部分生命周期命令的替身适配器。
type lifecycleFake struct {
	name string
	cmds []string
}

func (a *lifecycleFake) Name() string                { return a.name }
func (a *lifecycleFake) SupportedCommands() []string { return a.cmds }
func (a *lifecycleFake) Open(ctx context.Context, cfg device.Config, _ func(device.Event)) (device.Device, error) {
	return &fakeDevice{id: cfg.ID, done: make(chan struct{})}, nil
}

func TestSupportsGatesLifecycleCommands(t *testing.T) {
	sup := &supervisor{dcfg: DeviceCfg{ID: "d1"}, adapter: &lifecycleFake{name: "x", cmds: []string{"dump"}}}
	if !sup.supports("dump") {
		t.Fatal("白名单内的命令应放行")
	}
	if sup.supports("sync") {
		t.Fatal("白名单外的命令必须跳过：核心不得对无对时语义的适配器下发 sync")
	}
	if sup.supports("") {
		t.Fatal("空命令名必须跳过")
	}
	if sup.pollCommand() != DefaultPollCommand || sup.syncCommand() != DefaultSyncCommand {
		t.Fatalf("生命周期命令缺省值错误: %q %q", sup.pollCommand(), sup.syncCommand())
	}
	sup.dcfg.PollCommand = "status"
	sup.dcfg.SyncCommand = " align "
	if sup.pollCommand() != "status" || sup.syncCommand() != "align" {
		t.Fatalf("配置覆盖未生效: %q %q", sup.pollCommand(), sup.syncCommand())
	}
}

// TestNoHardwareAdapterSkipsSyncWithoutNoise 是无硬件参考设备的关键回归：
// demo 没有对时语义，edge 不得在打开时/对时周期上产生失败与噪声。
func TestNoHardwareAdapterSkipsSyncWithoutNoise(t *testing.T) {
	sup := &supervisor{dcfg: DeviceCfg{ID: "d1", SyncCommand: DefaultSyncCommand}, adapter: &lifecycleFake{name: "y", cmds: []string{"ping", "set", "dump", "noop"}}}
	if sup.supports(sup.syncCommand()) {
		t.Fatal("demo 形态的白名单不应包含 sync")
	}
	if !sup.supports(sup.pollCommand()) {
		t.Fatal("dump 在白名单内，轮询应照常触发状态读取")
	}
}

// TestSemanticDescriptorFingerprintIgnoresTimestamps 锁定 diff 抑制语义：
// 只有时间戳变化不得触发重发（否则每拍都会重发整份 Descriptor），
// 但观测值变化必须触发。
func TestSemanticDescriptorFingerprintIgnoresTimestamps(t *testing.T) {
	base := semanticTestDescriptor("01:48", time.Unix(1000, 0))
	newer := semanticTestDescriptor("01:48", time.Unix(2000, 0))
	changed := semanticTestDescriptor("01:49", time.Unix(2000, 0))

	if string(mustJSON(semanticDescriptor(base))) != string(mustJSON(semanticDescriptor(newer))) {
		t.Fatal("只有时间戳不同的 Descriptor 指纹必须相同（diff 抑制不能被时间戳击穿）")
	}
	if string(mustJSON(semanticDescriptor(base))) == string(mustJSON(semanticDescriptor(changed))) {
		t.Fatal("观测值变化必须产生不同指纹")
	}
	// 深拷贝：计算指纹不得污染真正要上报的 payload（时间戳必须保留）。
	if base.Entities[0].Observations["time"].ObservedAt.IsZero() {
		t.Fatal("semanticDescriptor 污染了原 Descriptor 的 observed_at")
	}
}
