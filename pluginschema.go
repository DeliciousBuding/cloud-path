// Package cloudpath 暴露仓库级只读契约资产的嵌入副本。
//
// 目的：发布出去的 CLI / Edge 二进制在**没有仓库 checkout 的机器**上也能校验插件
// manifest。否则「干净电脑 → 安装 Edge → 从 GitHub 安装 Driver」这条验收路径会卡在
// 找不到 spec/plugin-manifest.schema.json。
//
// SSOT 仍是 spec/plugin-manifest.schema.json；这里只是同一份文件的嵌入副本，
// 不允许出现第二份手写 schema。
package cloudpath

import _ "embed"

// PluginManifestSchema 是 spec/plugin-manifest.schema.json 的嵌入副本。
//
//go:embed spec/plugin-manifest.schema.json
var PluginManifestSchema []byte
