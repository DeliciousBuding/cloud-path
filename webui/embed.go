//go:build embed_ui

// Package webui 内嵌前端构建产物。
// task build 先构建 webui/dist，再以 -tags embed_ui 编译 server → 单二进制发布。
package webui

import "embed"

//go:embed all:dist
var Dist embed.FS
