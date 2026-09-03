// Package edge 是 Cloudpath 边缘代理运行时：串口设备监督（热插拔自愈）、
// 轮询/对时调度、状态上报与命令执行（WS 持久连接）。
package edge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是 edge 配置（edge.yaml，本地私有不入库）。
type Config struct {
	Server          string        `yaml:"server"`            // ws://host:port/ws/edge
	Token           string        `yaml:"token"`             // 支持 ${ENV} 展开
	EdgeID          string        `yaml:"edge_id"`           // 缺省用主机名
	PollIntervalS   int           `yaml:"poll_interval_s"`   // 转储轮询（默认 5）
	SyncIntervalS   int           `yaml:"sync_interval_s"`   // 对时周期（默认 600）
	ReportIntervalS int           `yaml:"report_interval_s"` // 状态心跳兜底（默认 30）
	Devices         []DeviceCfg   `yaml:"devices"`
	PluginHost      PluginHostCfg `yaml:"plugin_host"`
}

// DeviceCfg 是单台设备配置。
type DeviceCfg struct {
	ID      string `yaml:"id"`
	Adapter string `yaml:"adapter"` // 设备适配器名（如 stcb）
	Name    string `yaml:"name"`
	Port    string `yaml:"port"` // Windows: COM3；Linux: /dev/ttyUSB0
	Baud    int    `yaml:"baud"` // 默认 9600
}

// PluginHostCfg 配置可选的进程内外部 Driver Plugin Host。默认不启用，保持 P1
// 纯内置 adapter 行为；启用时按 desired-state + lockfile 启动外部 driver 实例。
type PluginHostCfg struct {
	Enabled       bool   `yaml:"enabled"`         // false=不启用外部 host（默认）
	Root          string `yaml:"root"`            // 插件安装根目录（如 plugins.d）
	StateDir      string `yaml:"state_dir"`       // 插件实例 desired-state 目录
	Tenant        string `yaml:"tenant"`          // 外部 driver 实例租户（缺省 default）
	Required      bool   `yaml:"required"`        // true=host 失败则 edge 启动失败；false=optional(DEGRADED)
	Lock          string `yaml:"lock"`            // plugins.lock 路径（缺省 <root>/plugins.lock）
	CloseTimeoutS int    `yaml:"close_timeout_s"` // host 优雅关闭 deadline（秒，默认 10）
}

// LoadConfig 读取并校验配置。Token 等字段支持 ${ENV_VAR} 展开（凭据不落盘）。
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w（请复制 edge.example.yaml 为 edge.yaml 并填写）", err)
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(raw))), &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if cfg.Server == "" {
		cfg.Server = "ws://127.0.0.1:8080/ws/edge"
	}
	if !strings.HasPrefix(cfg.Server, "ws://") && !strings.HasPrefix(cfg.Server, "wss://") {
		return nil, fmt.Errorf("server 须以 ws:// 或 wss:// 开头，got %q", cfg.Server)
	}
	if cfg.EdgeID == "" {
		hn, err := os.Hostname()
		if err != nil || hn == "" {
			hn = "edge"
		}
		cfg.EdgeID = strings.ToLower(strings.ReplaceAll(hn, ".", "-"))
	}
	if cfg.PollIntervalS <= 0 {
		cfg.PollIntervalS = 5
	}
	if cfg.SyncIntervalS <= 0 {
		cfg.SyncIntervalS = 600
	}
	if cfg.ReportIntervalS <= 0 {
		cfg.ReportIntervalS = 30
	}
	if len(cfg.Devices) == 0 {
		return nil, fmt.Errorf("devices 为空：至少配置一台设备")
	}
	seen := map[string]bool{}
	for i := range cfg.Devices {
		d := &cfg.Devices[i]
		if d.ID == "" {
			return nil, fmt.Errorf("devices[%d]: id 必填", i)
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("devices[%d]: id %q 重复", i, d.ID)
		}
		seen[d.ID] = true
		if d.Adapter == "" {
			return nil, fmt.Errorf("devices[%s]: adapter 必填", d.ID)
		}
		if d.Port == "" {
			return nil, fmt.Errorf("devices[%s]: port 必填（如 COM3 或 /dev/ttyUSB0）", d.ID)
		}
		if d.Baud <= 0 {
			d.Baud = 9600
		}
	}
	if err := cfg.validatePluginHost(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validatePluginHost 校验外部 Driver Plugin Host 配置。默认不启用时不校验；
// 启用时 fail-closed：root/state 必填，tenant/lock/close_timeout 提供默认值。
func (c *Config) validatePluginHost() error {
	ph := &c.PluginHost
	if !ph.Enabled {
		return nil
	}
	if strings.TrimSpace(ph.Root) == "" {
		return fmt.Errorf("plugin_host.root 必填（启用外部 Driver Plugin Host 时）")
	}
	if strings.TrimSpace(ph.StateDir) == "" {
		return fmt.Errorf("plugin_host.state_dir 必填（启用外部 Driver Plugin Host 时）")
	}
	if strings.TrimSpace(ph.Tenant) == "" {
		ph.Tenant = "default"
	}
	if strings.TrimSpace(ph.Lock) == "" {
		ph.Lock = filepath.Join(ph.Root, "plugins.lock")
	}
	if ph.CloseTimeoutS <= 0 {
		ph.CloseTimeoutS = 10
	}
	return nil
}
