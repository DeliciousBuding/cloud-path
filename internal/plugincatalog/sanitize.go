package plugincatalog

import (
	"regexp"
	"strings"
)

// maxDetailLen 是 Edge 上报错误摘要在 API 响应中的长度上限（control-plane-sync §4.3）。
const maxDetailLen = 256

var (
	// winPathPattern 命中盘符绝对路径（C:\foo\bar、C:/foo/bar）。
	winPathPattern = regexp.MustCompile(`[A-Za-z]:[\\/][^\s"']*`)
	// posixPathPattern 命中多段绝对路径（/var/lib/cloudpath/x.json）。
	posixPathPattern = regexp.MustCompile(`/(?:[\w.@-]+/)+[\w.@-]+`)
	// uncPathPattern 命中 UNC 路径（\\host\share）。
	uncPathPattern = regexp.MustCompile(`\\\\[\w.-]+(?:\\[^\s"']*)+`)
	// credentialPattern 命中 key=value 形式的疑似凭据片段。
	credentialPattern = regexp.MustCompile(`(?i)(password|passwd|pwd|token|secret|api[-_]?key|access[-_]?key|authorization|cookie)(["']?\s*[:=]\s*)([^\s,;}"']+)`)
)

// pathRedaction 是绝对路径的统一占位符（不泄漏本机目录结构）。
const pathRedaction = "[path]"

// credentialRedaction 是疑似凭据值的统一占位符。
const credentialRedaction = "[REDACTED]"

// SanitizeDetail 对 Edge 上报的错误摘要做防御性脱敏：截断长度、抹掉本机绝对路径
// 与疑似凭据值。Edge 侧本应已脱敏（control-plane-sync §2 不变量 6），这里是
// Server 不信任下游输入的第二道闸：任何响应体都不得泄漏 secret 明文或本机路径。
func SanitizeDetail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = credentialPattern.ReplaceAllString(s, "$1$2"+credentialRedaction)
	s = uncPathPattern.ReplaceAllString(s, pathRedaction)
	s = winPathPattern.ReplaceAllString(s, pathRedaction)
	s = posixPathPattern.ReplaceAllString(s, pathRedaction)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxDetailLen {
		s = strings.ToValidUTF8(s[:maxDetailLen], "") + "…"
	}
	return s
}
