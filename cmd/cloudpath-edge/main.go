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
	"syscall"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb" // 设备适配器注册
	"github.com/DeliciousBuding/cloud-path/internal/edge"
	"github.com/DeliciousBuding/cloud-path/internal/logx"
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

	if err := edge.Run(ctx, cfg, version); err != nil {
		slog.Error("edge exited with error", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
