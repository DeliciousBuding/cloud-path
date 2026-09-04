package registry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	cloudpath "github.com/DeliciousBuding/cloud-path"
)

// LoadManifestSchema 返回校验 plugin.yaml 用的 JSON Schema，并说明它来自哪里。
//
// path 非空且文件可读时用文件（运维可覆盖）；文件不存在则回落到二进制内嵌副本。
// 发布的 CLI 因此在没有仓库 checkout 的机器上也能完成 inspect / install / validate，
// 而不需要额外分发 schema 文件。其它读取错误（权限、目录、IO）照常失败，不静默降级。
func LoadManifestSchema(path string) (data []byte, source string, err error) {
	if path != "" {
		b, readErr := os.ReadFile(path)
		if readErr == nil {
			return b, "file:" + path, nil
		}
		if !errors.Is(readErr, fs.ErrNotExist) {
			return nil, "", fmt.Errorf("read manifest schema %s: %w", path, readErr)
		}
	}
	if len(cloudpath.PluginManifestSchema) == 0 {
		return nil, "", fmt.Errorf("read manifest schema %s: %w", path, fs.ErrNotExist)
	}
	return cloudpath.PluginManifestSchema, "embedded", nil
}
