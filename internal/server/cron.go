package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronExpr 是 5 字段标准 cron 表达式（minute hour dom month dow）的最小实现。
// 支持：* 、 */step 、 a 、 a-b 、 a-b/step 、 a,b,c 组合。
// 语义遵循 Vixie cron：dom 与 dow 都受限制时按 OR 匹配；时区由调用方传入
// （nextAfter 在 loc 内解释字段）。扫描法求下一触发（分钟粒度，上限 366 天）。
type cronExpr struct {
	minute, hour, dom, month, dow uint64 // 位集：bit i = 值 i 在集合内
	starDom, starDow              bool
}

const (
	cronMinuteBits = 60
	cronHourBits   = 24
	cronDomBits    = 32
	cronMonthBits  = 13
	cronDowBits    = 7
	cronScanLimit  = 366 * 24 * 60 // 求下一次触发的扫描上限（分钟）
)

// parseCronExpr 解析并校验 5 字段 cron 表达式。
func parseCronExpr(spec string) (*cronExpr, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: want 5 fields, got %d in %q", len(fields), spec)
	}
	e := &cronExpr{}
	var err error
	if e.minute, err = parseCronField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("cron minute: %w", err)
	}
	if e.hour, err = parseCronField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("cron hour: %w", err)
	}
	if e.dom, e.starDom, err = parseCronFieldStar(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("cron day-of-month: %w", err)
	}
	if e.month, err = parseCronField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("cron month: %w", err)
	}
	if e.dow, e.starDow, err = parseCronFieldStar(fields[4], 0, 6); err != nil {
		return nil, fmt.Errorf("cron day-of-week: %w", err)
	}
	if e.minute == 0 || e.hour == 0 || e.month == 0 || e.dom == 0 || e.dow == 0 {
		return nil, fmt.Errorf("cron: empty field set in %q", spec)
	}
	return e, nil
}

// parseCronField 解析单个数字字段为位集（不带 star 标记）。
func parseCronField(field string, min, max int) (uint64, error) {
	set, _, err := parseCronFieldStar(field, min, max)
	return set, err
}

// parseCronFieldStar 解析字段并额外报告是否为裸 *（dom/dow OR 语义需要）。
func parseCronFieldStar(field string, min, max int) (set uint64, star bool, err error) {
	if field == "*" {
		for v := min; v <= max; v++ {
			set |= 1 << uint(v)
		}
		return set, true, nil
	}
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return 0, false, fmt.Errorf("empty list item in %q", field)
		}
		lo, hi, step := min, max, 1
		rangePart := part
		if i := strings.Index(part, "/"); i >= 0 {
			rangePart = part[:i]
			step, err = strconv.Atoi(part[i+1:])
			if err != nil || step <= 0 {
				return 0, false, fmt.Errorf("bad step in %q", part)
			}
		}
		if rangePart == "*" {
			// */n：min..max
		} else if i := strings.Index(rangePart, "-"); i >= 0 {
			lo, err = strconv.Atoi(rangePart[:i])
			if err != nil {
				return 0, false, fmt.Errorf("bad range start in %q", part)
			}
			hi, err = strconv.Atoi(rangePart[i+1:])
			if err != nil {
				return 0, false, fmt.Errorf("bad range end in %q", part)
			}
		} else {
			lo, err = strconv.Atoi(rangePart)
			if err != nil {
				return 0, false, fmt.Errorf("bad value %q", part)
			}
			hi = lo
		}
		if lo < min || hi > max || lo > hi {
			return 0, false, fmt.Errorf("value out of range [%d,%d] in %q", min, max, part)
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, false, nil
}

// matches 报告 t（loc 内）是否命中表达式（分钟粒度）。
func (e *cronExpr) matches(t time.Time) bool {
	if e.minute&(1<<uint(t.Minute())) == 0 {
		return false
	}
	if e.hour&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if e.month&(1<<uint(int(t.Month()))) == 0 {
		return false
	}
	domOK := e.dom&(1<<uint(t.Day())) != 0
	dowOK := e.dow&(1<<uint(int(t.Weekday()))) != 0
	if e.starDom && e.starDow {
		return true
	}
	if e.starDom {
		return dowOK
	}
	if e.starDow {
		return domOK
	}
	return domOK || dowOK // Vixie cron：两者都限制时 OR
}

// nextAfter 返回严格晚于 from 的下一次触发时刻（分钟对齐，loc 时区）。
// 扫描上限 366 天（如 2 月 30 日这类永不触发的表达式返回零值时间）。
func (e *cronExpr) nextAfter(from time.Time, loc *time.Location) time.Time {
	t := from.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(time.Duration(cronScanLimit) * time.Minute)
	for t.Before(limit) {
		if e.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}
