package stcb

import (
	"testing"
	"time"
)

func TestParseDump(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
		dump Dump
	}{
		{"标准行", "S:01213120", true, Dump{State: 0, Hour: 12, Min: 13, Slots: [3]int{1, 2, 0}}},
		{"示例行", "S:00805111", true, Dump{State: 0, Hour: 8, Min: 5, Slots: [3]int{1, 1, 1}}},
		{"提醒中", "S:12359002", true, Dump{State: 1, Hour: 23, Min: 59, Slots: [3]int{0, 0, 2}}},
		{"逾期态", "S:21215120", true, Dump{State: 2, Hour: 12, Min: 15, Slots: [3]int{1, 2, 0}}},
		{"损坏分隔符", "S\ufffd01213120", true, Dump{State: 0, Hour: 12, Min: 13, Slots: [3]int{1, 2, 0}}},
		// 真实板测捕获：噪声前缀中抢救出合法转储（search 而非 anchor 的意义）
		{"噪声前缀 salvage", "O:21S:21213120", true, Dump{State: 2, Hour: 12, Min: 13, Slots: [3]int{1, 2, 0}}},
		{"hour越界(0x92垃圾)", "S:09213120", false, Dump{}},
		{"混入非数字", "S:0121A120", false, Dump{}},
		{"太短", "S:012131", false, Dump{}},
		{"事件行不误判", "REMIND", false, Dump{}},
		{"MISSED不误判", "MISSED", false, Dump{}},
		{"空行", "", false, Dump{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseDump(c.line)
			if ok != c.want {
				t.Fatalf("ParseDump(%q) ok=%v want %v", c.line, ok, c.want)
			}
			if ok {
				c.dump.Raw = got.Raw
				if got != c.dump {
					t.Fatalf("ParseDump(%q) = %+v, want %+v", c.line, got, c.dump)
				}
			}
		})
	}
}

func TestParseEvent(t *testing.T) {
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{"BOOT", "BOOT", true},
		{"VENDOR-BOOT", "BOOT", true}, // 厂商前缀由包含匹配归一
		{"REMIND", "REMIND", true},
		{"TAKEN", "TAKEN", true},
		{"TAKEN-LATE", "TAKEN-LATE", true}, // 长标签优先，不被 TAKEN 截胡
		{"LATE", "TAKEN-LATE", true},
		{"MISSED", "MISSED", true},
		{"OK", "SYNC-OK", true},
		{"[RAW-NOEOL] REMIND", "REMIND", true},
		{"S:01213120", "", false}, // 转储行不是事件
		{"", "", false},
		{"hello world", "", false},
	}
	for _, c := range cases {
		got, ok := ParseEvent(c.line)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseEvent(%q) = (%q,%v), want (%q,%v)", c.line, got, ok, c.want, c.ok)
		}
	}
}

func TestStateLabel(t *testing.T) {
	want := map[int]string{0: "待机", 1: "提醒中", 2: "逾期", 9: "未知"}
	for in, w := range want {
		if got := StateLabel(in); got != w {
			t.Errorf("StateLabel(%d) = %q, want %q", in, got, w)
		}
	}
}

func TestSlotLabel(t *testing.T) {
	want := map[int]string{0: "待确认", 1: "已确认", 2: "逾期", 9: "未知"}
	for in, w := range want {
		if got := SlotLabel(in); got != w {
			t.Errorf("SlotLabel(%d) = %q, want %q", in, got, w)
		}
	}
}

func TestDriftMin(t *testing.T) {
	bj := func(hh, mm int) time.Time {
		return time.Date(2026, 9, 3, hh, mm, 30, 0, time.FixedZone("UTC+8", 8*3600))
	}
	cases := []struct {
		name      string
		hour, min int
		bjH, bjM  int
		want      float64
	}{
		{"一致", 12, 13, 12, 13, 0},
		{"板快1分", 12, 14, 12, 13, 1},
		{"板慢1分", 12, 12, 12, 13, -1},
		{"午夜回绕(板23:59 北京00:01)", 23, 59, 0, 1, -2},
		{"午夜回绕反向(板00:01 北京23:59)", 0, 1, 23, 59, 2},
	}
	for _, c := range cases {
		if got := DriftMin(c.hour, c.min, bj(c.bjH, c.bjM)); got != c.want {
			t.Errorf("%s: DriftMin = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidHHMM(t *testing.T) {
	ok := []string{"0000", "2359", "1213"}
	bad := []string{"2400", "1260", "123", "12a3", ""}
	for _, s := range ok {
		if !validHHMM(s) {
			t.Errorf("validHHMM(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validHHMM(s) {
			t.Errorf("validHHMM(%q) = true, want false", s)
		}
	}
}
