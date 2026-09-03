// Package edge 是 Cloudpath 边缘代理运行时：串口设备监督（热插拔自愈）、
// 轮询/对时调度、状态上报与命令执行（WS 持久连接）。
package edge

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是 edge 配置（edge.yaml，本地私有不入库）。
type Config struct {
	Server          string      `yaml:"server"`            // ws://host:port/ws/edge
	Token           string      `yaml:"token"`             // 支持 ${ENV} 展开
	EdgeID          string      `yaml:"edge_id"`           // 缺省用主机名
	PollIntervalS   int         `yaml:"poll_interval_s"`   // 转储轮询（默认 5）
	SyncIntervalS   int         `yaml:"sync_interval_s"`   // 对时周期（默认 600）
	ReportIntervalS int         `yaml:"report_interval_s"` // 状态心跳兜底（默认 30）
	Devices         []DeviceCfg `yaml:"devices"`
}

// DeviceCfg 是单台设备配置。
type DeviceCfg struct {
	ID      string `yaml:"id"`
	Adapter string `yaml:"adapter"` // 设备适配器名（如 stcb）
	Name    string `yaml:"name"`
	Port    string `yaml:"port"` // Windows: COM3；Linux: /dev/ttyUSB0
	Baud    int    `yaml:"baud"` // 默认 9600
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
	return &cfg, nil
}
