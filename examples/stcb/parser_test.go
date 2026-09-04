package stcb

import (
	"math"
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

// frameSensorV 是一条合法 V 帧（hh=09 mm=30 ss=55 st=2 rt=0x080 rop=0x1CC nav=0x1E0
// ext0=0x2A0 ext1=0x100 hall=1 vib=0 key=0）。ADC 值全部在 0x000-0x3FF 内。
const frameSensorV = "V:09305520801CC1E02A0100100"

func TestParseSensor(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		want   bool
		sensor Sensor
	}{
		{"标准帧", frameSensorV, true, Sensor{Hour: 9, Min: 30, Sec: 55, State: 2, Rt: 0x080, Rop: 0x1CC, Nav: 0x1E0, Ext0: 0x2A0, Ext1: 0x100, Hall: 1, Vib: 0, Key: 0}},
		{"损坏分隔符", "V\ufffd09305520801CC1E02A0100100", true, Sensor{Hour: 9, Min: 30, Sec: 55, State: 2, Rt: 0x080, Rop: 0x1CC, Nav: 0x1E0, Ext0: 0x2A0, Ext1: 0x100, Hall: 1, Vib: 0, Key: 0}},
		{"噪声前缀 salvage", "O:21" + frameSensorV, true, Sensor{Hour: 9, Min: 30, Sec: 55, State: 2, Rt: 0x080, Rop: 0x1CC, Nav: 0x1E0, Ext0: 0x2A0, Ext1: 0x100, Hall: 1, Vib: 0, Key: 0}},
		{"小写hex", "V:09305520801cc1e02a0100100", true, Sensor{Hour: 9, Min: 30, Sec: 55, State: 2, Rt: 0x080, Rop: 0x1CC, Nav: 0x1E0, Ext0: 0x2A0, Ext1: 0x100, Hall: 1, Vib: 0, Key: 0}},
		{"hour越界(0x92垃圾)", "V:24305520801CC1E02A0100100", false, Sensor{}},
		{"ADC越界(>0x3FF)", "V:0930552FF11CC1E02A0100100", false, Sensor{}},
		{"hall越界(=2)", "V:09305520801CC1E02A010200", false, Sensor{}},
		{"转储行不误判", "S:01213120", false, Sensor{}},
		{"事件行不误判", "REMIND", false, Sensor{}},
		{"太短", "V:0930552", false, Sensor{}},
		{"空行", "", false, Sensor{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseSensor(c.line)
			if ok != c.want {
				t.Fatalf("ParseSensor(%q) ok=%v want %v", c.line, ok, c.want)
			}
			if ok {
				c.sensor.Raw = got.Raw
				if got != c.sensor {
					t.Fatalf("ParseSensor(%q) = %+v, want %+v", c.line, got, c.sensor)
				}
			}
		})
	}
}

func TestTempC(t *testing.T) {
	if v := TempC(128); v < 75 || v > 78 {
		t.Errorf("TempC(128) = %v, want ~76.3C", v)
	}
	if v := TempC(511); v < 24 || v > 26 {
		t.Errorf("TempC(511) = %v, want ~25C", v)
	}
	if v := TempC(1023); v > -20 {
		t.Errorf("TempC(1023) = %v, want 很冷（< -20C）", v)
	}
	if v := TempC(-1); !math.IsNaN(v) {
		t.Errorf("TempC(-1) = %v, want NaN", v)
	}
	if v := TempC(1024); !math.IsNaN(v) {
		t.Errorf("TempC(1024) = %v, want NaN", v)
	}
}
