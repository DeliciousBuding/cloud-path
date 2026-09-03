//go:build !embed_ui

package webui

import "embed"

// Dist 为零值：未启用 embed_ui 构建标签。
// 开发模式下 server 走 -webui 磁盘目录（Vite dev server 代理时甚至无需静态目录）。
var Dist embed.FS
