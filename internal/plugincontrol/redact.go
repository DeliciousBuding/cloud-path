package plugincontrol

import (
	"regexp"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
)

// DetailLimit 是所有出网 detail 字段的长度上限（字节）：command_ack 的执行结果
// 摘要、plugin_ack 的逐实例结果、plugin_status 的实例 detail 共用同一上限。
// detail 是「一行事实摘要」，不是日志：超长必须截断。
const DetailLimit = 240

var (
	// 折行/制表压成单空格：detail 不得携带多行日志或 stdout/stderr 原文。
	detailSpaceRE = regexp.MustCompile(`[\r\n\t\v\f]+`)
	// Windows 盘符绝对路径（反斜杠与正斜杠两种形态都命中）。
	detailWinPathRE = regexp.MustCompile(`[A-Za-z]:[\\/][^\s]*`)
	// 进程输出标记：stdout/stderr/log 之后的内容一律丢弃（不变量 6：
	// 上报不得回传插件 stdout/stderr 原文）。宁可少报，不可泄漏。
	detailOutputRE = regexp.MustCompile(`(?i)\b(?:stdout|stderr|log)\b\s*[:=].*$`)
	// POSIX 家目录/系统目录绝对路径。刻意**不含 /dev**：
	// /dev/ttyUSB0 是端口名，本来就作为 DeviceMeta.Port 公开，不是本机路径泄漏。
	detailPosixPathRE = regexp.MustCompile(`/(?:home|root|Users|tmp|var|opt|srv|mnt|media|data|app|etc|usr)/[^\s]*`)
)

// SanitizeDetail 把适配器/插件给出的摘要收敛成可安全上报的单行事实。
// 它是 Edge 全部出网 detail 的统一兜底（设备命令 ack 与插件控制面共用一份实现，
// 不各造一套脱敏规则）：
//
//  1. 折行与控制字符压成空格（禁止把进程 stdout/stderr 原文当 detail 回传）；
//  2. 复用 pluginhost.Redact 抹掉 secret / token / password / authorization 形态的值
//     （单一实现，不另造一份脱敏正则）；
//  3. 丢弃 stdout / stderr / log 标记之后的全部内容（进程输出原文一律不出网）；
//  4. 抹掉本机绝对路径（Windows 盘符路径与 POSIX 家目录/系统目录路径）；
//     端口名（COM3、/dev/ttyUSB0）保留——它是设备元数据里本来就公开的 port 字段；
//  5. UTF-8 安全截断到 DetailLimit。
//
// 上报红线（control-plane-sync.md 不变量 6 与本轮验收要求）由此函数统一兜底：
// 即使适配器返回了不合规摘要，出网的 detail 也已脱敏。
func SanitizeDetail(s string) string {
	s = strings.TrimSpace(detailSpaceRE.ReplaceAllString(s, " "))
	if s == "" {
		return ""
	}
	s = pluginhost.Redact(s)
	if loc := detailOutputRE.FindStringIndex(s); loc != nil {
		s = strings.TrimSpace(s[:loc[0]]) + " <output-suppressed>"
	}
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
