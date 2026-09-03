package edge

import (
	"regexp"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
)

// DetailLimit 是 command_ack / plugin_ack 的 detail 字段长度上限（字节）。
// detail 是「执行结果反馈」的一行事实摘要，不是日志：超长必须截断。
const DetailLimit = 240

var (
	// 折行/制表压成单空格：detail 不得携带多行日志或 stdout/stderr 原文。
	detailSpaceRE = regexp.MustCompile(`[\r\n\t\v\f]+`)
	// Windows 绝对路径（C:\Users\... 与 C:/Users/...）。
	detailWinPathRE = regexp.MustCompile(`[A-Za-z]:[\\/][^\s]*`)
	// POSIX 家目录/系统目录绝对路径。刻意**不含 /dev**：
	// /dev/ttyUSB0 是端口名，本来就作为 DeviceMeta.Port 公开，不是本机路径泄漏。
	detailPosixPathRE = regexp.MustCompile(`/(?:home|root|Users|tmp|var|opt|srv|mnt|media|data|app|etc|usr)/[^\s]*`)
)

// SanitizeDetail 把适配器/插件给出的执行摘要收敛成可安全上报的单行事实：
//
//  1. 折行与控制字符压成空格（禁止把进程 stdout/stderr 原文当 detail 回传）；
//  2. 复用 pluginhost.Redact 抹掉 secret / token / password / authorization 形态的值
//     （单一实现，不另造一份脱敏正则）；
//  3. 抹掉本机绝对路径（Windows 盘符路径与 POSIX 家目录/系统目录路径）；
//     端口名（COM3、/dev/ttyUSB0）保留——它是设备元数据里本来就公开的 port 字段；
//  4. UTF-8 安全截断到 DetailLimit。
//
// 上报红线（control-plane-sync.md 不变量 6 与本轮验收要求）由此函数统一兜底：
// 即使适配器返回了不合规摘要，出网的 detail 也已脱敏。
func SanitizeDetail(s string) string {
	s = strings.TrimSpace(detailSpaceRE.ReplaceAllString(s, " "))
	if s == "" {
		return ""
	}
	s = pluginhost.Redact(s)
	s = detailWinPathRE.ReplaceAllString(s, "<path>")
	s = detailPosixPathRE.ReplaceAllString(s, "<path>")
	s = strings.TrimSpace(s)
	return truncateUTF8(s, DetailLimit)
}

// truncateUTF8 按字节上限截断，且不切断 UTF-8 多字节字符；被截断时以 "..." 结尾。
func truncateUTF8(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit - 3
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "..."
}

// utf8Start 报告 b 是否是一个 UTF-8 字符的首字节（非 10xxxxxx 续字节）。
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
