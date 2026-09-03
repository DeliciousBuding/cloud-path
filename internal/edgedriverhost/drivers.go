package edgedriverhost

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/registry"
	"gopkg.in/yaml.v3"
)

// driverContributions 是 plugin.yaml 的 `contributes.drivers` 片段。
// 在 internal/registry 尚未建模 contributions 之前，本包就地解析原始 manifest，
// 与 docs/architecture/plugin-system.md §2 的 Manifest 结构保持一致。
type driverContributions struct {
	Contributes struct {
		Drivers []struct {
			ID string `yaml:"id"`
		} `yaml:"drivers"`
	} `yaml:"contributes"`
}

// DriverIDs 返回 lockfile 中每个已安装插件贡献的 driver ID（去重、排序）。
// 没有 `contributes.drivers` 块的 manifest（如 Application/Connector 或旧版
// Driver）贡献零个 ID；lock 条目对应的 manifest 无法读取/解析时 fail-closed。
func DriverIDs(root, lockPath string) ([]string, error) {
	lock, err := registry.LoadLockFile(lockPath)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, locked := range lock.Plugins {
		manifestPath := filepath.Join(root, registry.SafePluginID(locked.ID), "plugin.yaml")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("read manifest for %s: %w", locked.ID, err)
		}
		var contributes driverContributions
		if err := yaml.Unmarshal(data, &contributes); err != nil {
			return nil, fmt.Errorf("parse manifest for %s: %w", locked.ID, err)
		}
		for _, d := range contributes.Contributes.Drivers {
			id := strings.TrimSpace(d.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// CheckConflicts 拒绝任何与外部 driver ID 重名的内置 adapter。
// 冲突必须显式拒绝，绝不允许静默覆盖。
func CheckConflicts(builtins, externalDriverIDs []string) error {
	external := map[string]bool{}
	for _, id := range externalDriverIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			external[id] = true
		}
	}
	var conflicts []string
	for _, name := range builtins {
		if external[name] {
			conflicts = append(conflicts, name)
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	sort.Strings(conflicts)
	return fmt.Errorf("builtin adapter conflicts with external driver id: %s", strings.Join(conflicts, ", "))
}
