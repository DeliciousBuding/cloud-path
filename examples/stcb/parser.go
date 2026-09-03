package stcb

import (
	"regexp"
	"strings"
	"time"
)

// Dump 是一条状态转储的解析结果。
// 线上格式：S:<state><hour BCD 2位><min BCD 2位><三槽各1位>，共 8 位十进制半字节。
type Dump struct {
	State int    // 0=待机 1=提醒中 2=逾期
	Hour  int    // 软件 hour（0-23）
	Min   int    // 芯片 min（0-59）
	Slots [3]int // 每槽 0=待确认 1=已确认 2=逾期
	Raw   string // 原始行（取证）
}

// dumpRe 与板测工具同源：允许 S 与 8 位数字之间出现损坏字符（如 ':' 变 U+FFFD）。
var dumpRe = regexp.MustCompile(`S[:\x{FFFD}]?(.{8})`)

// ParseDump 解析转储行。响铃期损坏行/非转储行返回 false（上层忽略即可）。
func ParseDump(line string) (Dump, bool) {
	if !strings.Contains(line, "S") {
		return Dump{}, false
	}
	m := dumpRe.FindStringSubmatch(line)
	if m == nil {
		return Dump{}, false
	}
	d := m[1]
	for i := 0; i < 8; i++ {
		if d[i] < '0' || d[i] > '9' {
			return Dump{}, false
		}
	}
	dump := Dump{
		State: int(d[0] - '0'),
		Hour:  int(d[1]-'0')*10 + int(d[2]-'0'),
		Min:   int(d[3]-'0')*10 + int(d[4]-'0'),
		Slots: [3]int{int(d[5] - '0'), int(d[6] - '0'), int(d[7] - '0')},
		Raw:   line,
	}
	// 语义合法性：hour<=23 min<=59 槽位<=2（损坏数字行大概率越界，直接拒绝）
	if dump.Hour > 23 || dump.Min > 59 || dump.State > 2 {
		return Dump{}, false
	}
	for _, s := range dump.Slots {
		if s > 2 {
			return Dump{}, false
		}
	}
	return dump, true
}

// 线上标签 → 规范化事件类型。不同固件版本可能用缩短标签（BOOT/LATE/OK），
// 也可能带厂商前缀（如 "XXXX-BOOT"）——由 ParseEvent 的包含匹配兜底归一。
// 匹配顺序敏感：长标签在前，防止 TAKEN-LATE 被 TAKEN 抢先截胡。
var eventTags = []struct {
	tag       string
	canonical string
}{
	{"TAKEN-LATE", "TAKEN-LATE"},
	{"REMIND", "REMIND"},
	{"TAKEN", "TAKEN"},
	{"MISSED", "MISSED"},
	{"BOOT", "BOOT"},
	{"LATE", "TAKEN-LATE"},
	{"OK", "SYNC-OK"},
}

// ParseEvent 把事件行规范化（缩短标签与全名归一）。非事件行返回 false。
func ParseEvent(line string) (string, bool) {
	t := strings.TrimSpace(line)
	for _, e := range eventTags {
		if t == e.tag {
			return e.canonical, true
		}
	}
	// 容忍前后缀噪声（如 "[RAW-NOEOL] REMIND"、"VENDOR-BOOT"）：包含匹配
	for _, e := range eventTags {
		if strings.Contains(t, e.tag) && !strings.HasPrefix(t, "S") {
			return e.canonical, true
		}
	}
	return "", false
}

// StateLabel 状态机中文标签（设备无关的业务语义由适配器负责本地化）。
func StateLabel(s int) string {
	switch s {
	case 0:
		return "待机"
	case 1:
		return "提醒中"
	case 2:
		return "逾期"
	default:
		return "未知"
	}
}

// SlotLabel 槽位中文标签。
func SlotLabel(s int) string {
	switch s {
	case 0:
		return "待确认"
	case 1:
		return "已确认"
	case 2:
		return "逾期"
	default:
		return "未知"
	}
}

// BeijingNow 返回北京时间（Asia/Shanghai；tzdata 缺失时退化为固定 UTC+8）。
func BeijingNow() time.Time {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return time.Now().In(loc)
	}
	return time.Now().In(time.FixedZone("UTC+8", 8*3600))
}

// DriftMin 计算板钟相对北京时间的漂移（分钟，回绕到 [-720,720)）。
// 板钟只有时/分两位，精度天然 ±1min。
func DriftMin(hour, min int, bj time.Time) float64 {
	board := hour*60 + min
	now := bj.Hour()*60 + bj.Minute()
	diff := board - now
	diff = ((diff+720)%1440 + 1440) % 1440 // Go 负数取模防护
	diff -= 720
	return float64(diff)
}
