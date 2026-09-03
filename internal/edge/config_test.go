package edge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "edge.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigDefaults(t *testing.T) {
	p := writeCfg(t, `
server: ws://127.0.0.1:8080/ws/edge
devices:
  - id: d1
    adapter: stcb
    port: COM3
`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.PollIntervalS != 5 || cfg.SyncIntervalS != 600 || cfg.ReportIntervalS != 30 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if cfg.Devices[0].Baud != 9600 {
		t.Fatalf("baud default = %d", cfg.Devices[0].Baud)
	}
	if cfg.EdgeID == "" {
		t.Fatal("edge_id should fall back to hostname")
	}
	if strings.Contains(cfg.EdgeID, ".") {
		t.Fatalf("edge_id 不应含点号（设备键用 / 分隔，主机名点号需归一）: %q", cfg.EdgeID)
	}
}

func TestLoadConfigEnvExpansion(t *testing.T) {
	t.Setenv("CP_TEST_TOKEN", "s3cret")
	p := writeCfg(t, `
server: wss://example.test/ws/edge
token: ${CP_TEST_TOKEN}
edge_id: lab-1
devices:
  - id: d1
    adapter: stcb
    port: /dev/ttyUSB0
    baud: 115200
`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "s3cret" {
		t.Fatalf("token env not expanded: %q", cfg.Token)
	}
	if cfg.EdgeID != "lab-1" || cfg.Devices[0].Baud != 115200 {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	cases := map[string]string{
		"缺 server 协议": `
server: http://127.0.0.1:8080
devices: [{id: d1, adapter: stcb, port: COM3}]
`,
		"devices 为空": `
server: ws://127.0.0.1:8080/ws/edge
devices: []
`,
		"缺 id": `
server: ws://x/ws/edge
devices: [{adapter: stcb, port: COM3}]
`,
		"id 重复": `
server: ws://x/ws/edge
devices:
  - {id: d1, adapter: stcb, port: COM3}
  - {id: d1, adapter: stcb, port: COM4}
`,
		"缺 adapter": `
server: ws://x/ws/edge
devices: [{id: d1, port: COM3}]
`,
		"缺 port": `
server: ws://x/ws/edge
devices: [{id: d1, adapter: stcb}]
`,
		"yaml 语法错": `
server: ws://x/ws/edge
devices: [{id: d1, adapter: stcb, port: COM3
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeCfg(t, body)); err == nil {
				t.Fatalf("%s: 期望报错，实际通过", name)
			}
		})
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("missing file should error")
	}
	if !strings.Contains(err.Error(), "edge.example.yaml") {
		t.Fatalf("错误信息应指引复制示例配置: %v", err)
	}
}

func TestExternalHostDisabledByDefault(t *testing.T) {
	p := writeCfg(t, `
server: ws://127.0.0.1:8080/ws/edge
devices:
  - id: d1
    adapter: stcb
    port: COM3
`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PluginHost.Enabled {
		t.Fatal("外部 Driver Plugin Host 应默认禁用")
	}
}

func TestExternalHostEnabledDefaults(t *testing.T) {
	p := writeCfg(t, `
server: ws://127.0.0.1:8080/ws/edge
devices:
  - id: d1
    adapter: stcb
    port: COM3
plugin_host:
  enabled: true
  root: plugins.d
  state_dir: data/plugin-state
`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	ph := cfg.PluginHost
	if !ph.Enabled {
		t.Fatal("应启用")
	}
	if ph.Tenant != "default" {
		t.Fatalf("tenant 默认值 = %q, want default", ph.Tenant)
	}
	if ph.Lock != filepath.Join("plugins.d", "plugins.lock") {
		t.Fatalf("lock 默认值 = %q", ph.Lock)
	}
	if ph.CloseTimeoutS != 10 {
		t.Fatalf("close_timeout_s 默认值 = %d, want 10", ph.CloseTimeoutS)
	}
}

func TestExternalHostEnabledFailClosed(t *testing.T) {
	cases := map[string]string{
		"缺 root": `
server: ws://x/ws/edge
devices: [{id: d1, adapter: stcb, port: COM3}]
plugin_host: {enabled: true, state_dir: data/plugin-state}
`,
		"缺 state_dir": `
server: ws://x/ws/edge
devices: [{id: d1, adapter: stcb, port: COM3}]
plugin_host: {enabled: true, root: plugins.d}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeCfg(t, body)); err == nil {
				t.Fatalf("%s: 期望报错，实际通过", name)
			}
		})
	}
}
