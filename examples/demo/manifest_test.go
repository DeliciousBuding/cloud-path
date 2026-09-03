package demo

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// 本测试把 plugin.yaml（拆仓冻结契约）与代码常量交叉锁定：
// - 六个必填标量字段逐行存在；
// - capabilities 声明与 Adapter.Capabilities() 目录完全一致（机器 ID 不漂移）；
// - contributes.drivers 的贡献 ID 等于注册的适配器名（demo）；
// - permissions.hardware 必须为空（诚实性红线：无硬件）；
// - 文件系统相关 ID 无路径分隔符/空白，capability ID 形态稳定。
// 任何一方改动导致失配都必须同时、显式地更新另一端，防止悄悄漂移。
func TestPluginManifestFrozenContract(t *testing.T) {
	data, err := os.ReadFile("plugin.yaml")
	if err != nil {
		t.Fatalf("read plugin.yaml: %v", err)
	}
	lines := strings.Split(string(data), "\n")

	required := []string{
		"apiVersion: plugins.cloudpath.dev/v1alpha1",
		"kind: Driver",
		"id: io.github.deliciousbuding.cloud-path-driver-demo",
		"version: 0.1.0",
		"protocol: 1",
		"entrypoint: cloudpath-driver-demo",
	}
	for _, want := range required {
		if !hasManifestLine(lines, want) {
			t.Errorf("plugin.yaml 缺少冻结标量行 %q", want)
		}
	}

	if !hasManifestLine(lines, "hardware: []") {
		t.Error("permissions.hardware 必须为空：参考演示设备不得声明任何硬件权限")
	}

	declaredCaps := manifestCapabilities(lines)
	catalog := map[string]bool{}
	for _, c := range (&Adapter{}).Capabilities() {
		catalog[c.Metadata.ID] = true
	}
	if len(declaredCaps) != len(catalog) {
		t.Fatalf("plugin.yaml capabilities = %d 项，代码 catalog = %d 项", len(declaredCaps), len(catalog))
	}
	for _, id := range declaredCaps {
		if !catalog[id] {
			t.Errorf("plugin.yaml 声明了代码 catalog 未提供的 capability %q", id)
		}
	}
	for id := range catalog {
		if !containsString(declaredCaps, id) {
			t.Errorf("代码 catalog capability %q 未在 plugin.yaml 声明", id)
		}
	}

	driverID := manifestDriverContributionID(lines)
	if driverID == "" {
		t.Fatal("plugin.yaml contributes.drivers 缺少 id")
	}
	if driverID != (&Adapter{}).Name() {
		t.Fatalf("contribution driver id = %q, Adapter.Name() = %q", driverID, (&Adapter{}).Name())
	}

	for _, id := range []string{
		"io.github.deliciousbuding.cloud-path-driver-demo",
		"cloudpath-driver-demo",
		driverID,
	} {
		if err := checkStableID(id); err != nil {
			t.Errorf("机器 ID %q 不稳定: %v", id, err)
		}
	}
	for _, id := range declaredCaps {
		if err := checkCapabilityID(id); err != nil {
			t.Errorf("capability ID %q 不稳定: %v", id, err)
		}
	}
}

func hasManifestLine(lines []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, line := range lines {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// manifestCapabilities 收集 capabilities: 块内的 `- <id>` 条目，直到下一个顶层键。
func manifestCapabilities(lines []string) []string {
	var out []string
	inBlock := false
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "capabilities:" {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			continue
		}
		if trimmed != "" && !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") {
			inBlock = false
		}
	}
	return out
}

// manifestDriverContributionID 取 contributes: -> drivers: 后第一个 `- id:` 值。
func manifestDriverContributionID(lines []string) string {
	afterDrivers := false
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if afterDrivers {
			if rest, ok := strings.CutPrefix(trimmed, "- id:"); ok {
				return strings.TrimSpace(rest)
			}
			if trimmed != "" && !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") {
				return ""
			}
			continue
		}
		if trimmed == "drivers:" {
			afterDrivers = true
		}
	}
	return ""
}

var (
	errManifestEmptyID  = errors.New("机器 ID 为空")
	errManifestUnsafeID = errors.New("机器 ID 含路径分隔符或空白")
)

func checkCapabilityID(id string) error {
	if id == "" {
		return errManifestEmptyID
	}
	if strings.ContainsAny(id, " \t\r\n") {
		return errors.New("capability ID 含空白")
	}
	const prefix = "cloudpath.dev/capability/"
	name, ok := strings.CutPrefix(id, prefix)
	if !ok || name == "" {
		return errors.New("capability ID 缺少 cloudpath.dev/capability/ 命名空间前缀")
	}
	at := strings.LastIndexByte(name, '@')
	if at <= 0 || at == len(name)-1 {
		return errors.New("capability ID 缺少 @N 语义版本尾缀")
	}
	digits := name[at+1:]
	if digits[0] == '0' {
		return errors.New("capability ID 版本尾缀含前导零")
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return errors.New("capability ID 版本尾缀不是正整数")
		}
	}
	return nil
}

func checkStableID(id string) error {
	if id == "" {
		return errManifestEmptyID
	}
	if strings.ContainsAny(id, "/\\ \t\r\n") {
		return errManifestUnsafeID
	}
	return nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
