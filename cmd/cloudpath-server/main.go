// cloudpath-server 是 Cloudpath 中心服务：REST API + WS hub + SQLite + 内嵌 WebUI。
//
// 用法：
//
//	cloudpath-server [-addr 127.0.0.1:8080] [-db data/cloudpath.db] [-token XXX] [-webui webui/dist]
//
// 环境变量：CLOUDPATH_ADDR / CLOUDPATH_DB / CLOUDPATH_TOKEN / CLOUDPATH_WEBUI /
// CLOUDPATH_RETENTION_DAYS / CLOUDPATH_CMD_RATE / CLOUDPATH_LOG / CLOUDPATH_LOG_FORMAT
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/DeliciousBuding/cloudpath/examples/stcb" // 适配器注册（命令白名单校验用）
	"github.com/DeliciousBuding/cloudpath/internal/logx"
	"github.com/DeliciousBuding/cloudpath/internal/server"
	"github.com/DeliciousBuding/cloudpath/internal/store"
)

// version 由构建注入：-ldflags "-X main.version=vX.Y.Z"
var version = "dev"

func main() {
	addr := flag.String("addr", envOr("CLOUDPATH_ADDR", "127.0.0.1:8080"), "HTTP 监听地址")
	dbPath := flag.String("db", envOr("CLOUDPATH_DB", "data/cloudpath.db"), "SQLite 数据库路径")
	token := flag.String("token", os.Getenv("CLOUDPATH_TOKEN"), "共享令牌（空=无鉴权，仅限本机/内网）")
	webuiDir := flag.String("webui", os.Getenv("CLOUDPATH_WEBUI"), "开发模式：前端静态目录（优先于内嵌产物）")
	logLevel := flag.String("log-level", envOr("CLOUDPATH_LOG", "info"), "日志级别 debug|info|warn|error")
	logFormat := flag.String("log-format", envOr("CLOUDPATH_LOG_FORMAT", "text"), "日志格式 text|json")
	retentionDays := flag.Int("retention-days", envInt("CLOUDPATH_RETENTION_DAYS", 30),
		"事件/命令保留天数，超期由后台清理（<=0 用默认 30）")
	cmdRate := flag.Int("cmd-rate", envInt("CLOUDPATH_CMD_RATE", 20),
		"单设备每分钟命令下发上限（防跑飞的 UI/脚本刷串口）")
	origins := flag.String("allowed-origins", os.Getenv("CLOUDPATH_ALLOWED_ORIGINS"),
		"WS 允许的浏览器 Origin 模式，逗号分隔（留空=开发策略：同源+localhost）")
	flag.Parse()
	logx.Setup(*logLevel, *logFormat)

	st, err := store.Open(*dbPath)
	if err != nil {
		slog.Error("open store failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := server.New(server.Config{
		Store: st, Token: *token, Version: version, WebUIDir: *webuiDir,
		RetentionDays: *retentionDays, CmdRatePerMin: *cmdRate,
		AllowedOrigins: splitList(*origins),
	})
	if len(splitList(*origins)) == 0 {
		slog.Warn("WS Origin 策略为开发模式（同源 + localhost）：公网部署请用 -allowed-origins 显式收紧")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.RunSweeper(ctx)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Info("cloudpath-server listening", "addr", *addr, "db", *dbPath, "version", version,
			"auth", *token != "", "retention_days", *retentionDays, "cmd_rate_per_min", *cmdRate,
			"origins", splitList(*origins))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	srv.CloseAll() // 先断 WS 长连接，避免 Shutdown 挂等
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		_ = httpSrv.Close()
	}
	slog.Info("bye")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// splitList 拆逗号分隔清单（去空白、丢空项）。
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// envInt 读整型环境变量：缺失或非法回退默认值（配置错误不应让服务起不来）。
func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid int env, using default", "key", key, "value", v, "default", def)
		return def
	}
	return n
}
