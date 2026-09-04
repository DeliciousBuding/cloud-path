// cloudpath-edge 是 Cloudpath 边缘代理：管理本机串口设备并接入 server。
//
// 用法：
//
//	cloudpath-edge [-config edge.yaml]
//
// 环境变量：CLOUDPATH_EDGE_CONFIG / CLOUDPATH_LOG；配置内 token 支持 ${ENV} 展开。
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/DeliciousBuding/cloud-path/examples/demo" // 参考演示设备适配器（无硬件，见 examples/demo/README.md）
	"github.com/DeliciousBuding/cloud-path/internal/edge"
	"github.com/DeliciousBuding/cloud-path/internal/edgedriverhost"
	"github.com/DeliciousBuding/cloud-path/internal/logx"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
)

// version 由构建注入：-ldflags "-X main.version=vX.Y.Z"
var version = "dev"

func main() {
	configPath := flag.String("config", envOr("CLOUDPATH_EDGE_CONFIG", "edge.yaml"), "edge 配置文件")
	logLevel := flag.String("log-level", envOr("CLOUDPATH_LOG", "info"), "日志级别 debug|info|warn|error")
	logFormat := flag.String("log-format", envOr("CLOUDPATH_LOG_FORMAT", "text"), "日志格式 text|json")
	flag.Parse()
	logx.Setup(*logLevel, *logFormat)

	cfg, err := edge.LoadConfig(*configPath)
	if err != nil {
		slog.Error("load config failed", "path", *configPath, "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := []edge.RunOption{}
	if cfg.PluginHost.Enabled {
		host, err := newDriverHost(cfg.PluginHost)
		if err != nil {
			slog.Error("external driver host config failed", "err", err)
			os.Exit(1)
		}
		opts = append(opts, edge.WithPluginHost(host))
		// 插件控制面同步（control-plane-sync.md §3.2/§4/§7/§8）：收 plugin_desired、
		// 按单调 revision 收敛、回 plugin_ack，并周期/重连上报 plugin_status。
		syncer, err := newPluginSyncer(cfg.PluginHost, host)
		if err != nil {
			slog.Error("plugin control plane config failed", "err", err)
			os.Exit(1)
		}
		opts = append(opts, edge.WithPluginSync(syncer))
	}

	if err := edge.Run(ctx, cfg, version, opts...); err != nil {
		slog.Error("edge exited with error", "err", err)
		os.Exit(1)
	}
}

// newDriverHost 用生产实现装配外部 Driver Plugin Host。
func newDriverHost(cfg edge.PluginHostCfg) (*edgedriverhost.Host, error) {
	return edgedriverhost.New(edgedriverhost.Options{
		Runner:     pluginhost.ExecRunner{},
		PluginsDir: cfg.Root,
		StateDir:   cfg.StateDir,
		LockPath:   cfg.Lock,
		Tenant:     cfg.Tenant,
		Logger:     slog.Default(),
		// 本地 secret provider：明文只存在于 <secret_dir>/<tenant>/<instance>/<name>，
		// 权限与路径安全由 internal/secrethandle 强制；未配置 secret_dir 时不提供明文，
		// 绑定 handle 的实例一律 fail-closed。
		Secrets:      newSecrets(cfg),
		CloseTimeout: time.Duration(cfg.CloseTimeoutS) * time.Second,
	})
}

// newSecrets 构造本地文件 secret provider；secret_dir 为空时返回 nil（fail-closed）。
func newSecrets(cfg edge.PluginHostCfg) plugincontrol.SecretResolver {
	if strings.TrimSpace(cfg.SecretDir) == "" {
		return nil
	}
	return plugincontrol.NewFileSecrets(cfg.SecretDir, cfg.Root)
}

// newPluginSyncer 装配 Edge 侧插件控制面收敛器。
// applied cache 落在 sync_state（缺省 <state_dir>/applied.json）：进程重启后据此
// 拒绝旧 revision；boot_id 每次进程启动都换新，绝不从缓存恢复。
func newPluginSyncer(cfg edge.PluginHostCfg, host *edgedriverhost.Host) (*plugincontrol.Syncer, error) {
	return plugincontrol.NewSyncer(plugincontrol.SyncOptions{
		Tenant:    cfg.Tenant,
		CachePath: cfg.SyncState,
		Applier:   host,
		Logger:    slog.Default(),
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
