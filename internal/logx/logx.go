// Package logx 提供 slog 全局初始化（结构化日志，零第三方依赖）。
package logx

import (
	"log/slog"
	"os"
	"strings"
)

// Setup 初始化全局 slog 默认 logger。level: debug|info|warn|error；format: text|json。
func Setup(level, format string) {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	if strings.ToLower(format) == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}
