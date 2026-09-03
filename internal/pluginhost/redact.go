package pluginhost

import "regexp"

// Sensitive field shapes. Values for these keys must never reach logs.
var (
	headerRedactRE = regexp.MustCompile("(?i)\\b(authorization|proxy-authorization)\\b\\s*[:=]\\s*[^\\r\\n]+")
	jsonRedactRE   = regexp.MustCompile("(?i)(\"(?:secret[_-]?key|secret|access[_-]?token|token|password|passwd|api[_-]?key|apikey|cookie|credential|proof|authorization)\"\\s*:\\s*)(?:\"[^\"]*\"|null|true|false|-?[0-9.]+)")
	kvRedactRE     = regexp.MustCompile("(?i)(\\b(?:secret[_-]?key|secret|access[_-]?token|token|password|passwd|api[_-]?key|apikey|cookie|credential|proof)\\b\\s*[:=]\\s*)(?:\"[^\"]*\"|[^\\s,;]+)")
)

// Redact removes values of sensitive fields from a single log line. It covers
// header style (Authorization: Bearer x), key/value style (password=hunter2)
// and JSON style ("token":"abc"). The replacement is stable so callers and
// tests can recognize the redaction point.
func Redact(line string) string {
	line = headerRedactRE.ReplaceAllString(line, "$1=[REDACTED]")
	line = jsonRedactRE.ReplaceAllString(line, "$1[REDACTED]")
	line = kvRedactRE.ReplaceAllString(line, "$1[REDACTED]")
	return line
}
