package edgedriverhost

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

// DriverIDs 返回 lockfile 中每个已安装插件贡献的 driver ID（去重、排序）。
// 贡献来自 registry.Manifest.Contributes 的 typed model——本包不再就地解析
// plugin.yaml，消除与 internal/registry 的二次解析漂移。
//
// 没有 `contributes.drivers` 块的 manifest（如 Application/Connector，或旧版
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
		manifest, err := registry.ReadManifest(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("read manifest for %s: %w", locked.ID, err)
		}
		if manifest.Contributes == nil {
			continue
		}
		for _, d := range manifest.Contributes.Drivers {
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
