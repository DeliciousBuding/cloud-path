package edge

import (
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
)

// DetailLimit 是 command_ack.detail 的长度上限（与插件控制面共用同一上限）。
const DetailLimit = plugincontrol.DetailLimit

// SanitizeDetail 把适配器给出的命令执行摘要收敛成可安全上报的单行事实。
//
// 实现只有一份，在 internal/plugincontrol（Edge 的设备面与插件面共用同一条上报
// 红线，不各造一套脱敏规则）；本包保留同名薄封装，让设备命令路径读起来是本地语义。
//
// 红线：不含明文 secret / 访问令牌 / 本机绝对路径 / 进程 stdout、stderr 原文；
// 端口名（COM3、/dev/ttyUSB0）是公开的设备元数据，刻意保留。
func SanitizeDetail(s string) string { return plugincontrol.SanitizeDetail(s) }
