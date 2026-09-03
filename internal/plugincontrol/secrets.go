package plugincontrol

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/registry"
	"github.com/DeliciousBuding/cloud-path/internal/secrethandle"
)

// ErrSecretForbidden 表示实例配置绑定的 secret handle 不可用：未声明、不存在、
// 已吊销、路径不安全或文件权限不合规。映射到稳定错误码
// api.PluginErrSecretForbidden，实例 reconcile 一律 fail-closed。
var ErrSecretForbidden = errors.New("plugincontrol: secret forbidden")

// SecretResolver 在 Edge 本地把 secret://<name> handle 解析成明文
// （control-plane-sync.md §7）。
//
// 边界：Server 只见 handle，永不接收或解析明文；明文只存在于 Edge 本地 provider
// 与目标插件进程的短期内存。本接口的实现**不得缓存明文**——handle 被吊销后
// 插件重启必须重新解析并失败，绝不能从任何本地缓存恢复旧明文。
type SecretResolver interface {
	// Resolve 返回 config 里显式绑定的每个 handle 的明文（键 = handle 串）。
	// 任一 handle 不可用即整体失败：不部分解析、不回落旧明文。
	// 返回的错误文本只含 handle 名与稳定原因码，绝不含明文或本机绝对路径。
	Resolve(tenant, instanceID, pluginID string, config map[string]string) (map[string][]byte, error)
}

// FileSecrets 是文件实现：明文位于 Root/<tenant>/<instance>/<name>，
// 按 tenant/instance 隔离；权限与路径安全由 internal/secrethandle 强制
// （POSIX 要求 0600 等价权限，越界/符号链接/超大文件一律拒绝）。
type FileSecrets struct {
	Root     string
	MaxBytes int64
	// PluginsDir 用于读取插件 manifest 的 permissions.secrets（双校验的另一半）。
	PluginsDir string
	// Declared 返回插件声明的 secret 名；nil 时用 declaredFromManifest。
	// 注入点：测试无需真实插件安装树。
	Declared func(pluginID string) ([]string, error)
}

// NewFileSecrets 构造文件 secret provider。
func NewFileSecrets(root, pluginsDir string) *FileSecrets {
	f := &FileSecrets{Root: root, PluginsDir: pluginsDir}
	f.Declared = f.declaredFromManifest
	return f
}

// BoundHandles 返回 config 中显式绑定的 secret:// handle（排序、去重）。
// 只返回引用，永不返回值——这是「实例配置显式绑定」这一半校验的事实源，
// 也可安全用于日志与上报。
func BoundHandles(config map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range config {
		raw := strings.TrimSpace(v)
		if !strings.HasPrefix(raw, secrethandle.Scheme) || seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
	}
	sort.Strings(out)
	return out
}

// Resolve 对每个绑定的 handle 做**双校验**后读取明文：
//  1. 插件 manifest 已声明该 secret 名（permissions.secrets）；
//  2. 实例配置显式绑定了该 handle（BoundHandles 给出）。
//
// 任一条不满足、或 handle 不存在/已吊销（文件被删除即视为吊销）、路径不安全、
// 文件权限不合规 → 返回 ErrSecretForbidden 并 fail-closed，绝不回落旧明文缓存
// （本实现不持有任何缓存，每次都重新读盘）。
func (f *FileSecrets) Resolve(tenant, instanceID, pluginID string, config map[string]string) (map[string][]byte, error) {
	handles := BoundHandles(config)
	if len(handles) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(f.Root) == "" {
		return nil, fmt.Errorf("%w: 本地 secret provider 未配置（secret_dir 为空）", ErrSecretForbidden)
	}
	declared, err := f.declared(pluginID)
	if err != nil {
		return nil, fmt.Errorf("%w: 无法读取插件 %s 的 secret 声明: %v", ErrSecretForbidden, pluginID, reasonOf(err))
	}
	provider := secrethandle.FileProvider{Root: f.Root, MaxBytes: f.MaxBytes}
	out := make(map[string][]byte, len(handles))
	for _, handle := range handles {
		plain, err := provider.Resolve(tenant, instanceID, handle, declared)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s", ErrSecretForbidden, handle, secretReason(err))
		}
		out[handle] = plain
	}
	return out, nil
}

func (f *FileSecrets) declared(pluginID string) ([]string, error) {
	if f.Declared != nil {
		return f.Declared(pluginID)
	}
	return f.declaredFromManifest(pluginID)
}

// declaredFromManifest 读已安装插件的 plugin.yaml，取 permissions.secrets。
func (f *FileSecrets) declaredFromManifest(pluginID string) ([]string, error) {
	if strings.TrimSpace(f.PluginsDir) == "" {
		return nil, fmt.Errorf("%w: plugins dir is required to verify declared secrets", ErrInvalidState)
	}
	if !validStateSegment(pluginID) && !strings.Contains(pluginID, ".") {
		return nil, fmt.Errorf("%w: plugin id %q", ErrInvalidState, pluginID)
	}
	path := filepath.Join(f.PluginsDir, registry.SafePluginID(pluginID), "plugin.yaml")
	manifest, err := registry.ReadManifest(path)
	if err != nil {
		return nil, err
	}
	return manifest.Permissions.Secrets, nil
}

// secretReason 把底层错误归一成稳定、非敏感的原因码。
//
// 刻意不复述底层错误文本：os/路径错误会带上本机绝对路径（例如
// "open /root/secrets/default/i1/key: no such file"），那正是上报红线禁止的内容。
func secretReason(err error) string {
	switch {
	case errors.Is(err, secrethandle.ErrUndeclaredSecret):
		return "secret_not_declared"
	case errors.Is(err, secrethandle.ErrInvalidHandle):
		return "secret_invalid_handle"
	case errors.Is(err, secrethandle.ErrUnsafeSecretPath):
		return "secret_unsafe_path"
	case errors.Is(err, secrethandle.ErrInsecurePermission):
		return "secret_insecure_permission"
	case errors.Is(err, secrethandle.ErrSecretTooLarge):
		return "secret_too_large"
	case errors.Is(err, os.ErrNotExist), errors.Is(err, os.ErrPermission):
		// 文件不存在即视为「未配置或已吊销」：两者都必须 fail-closed。
		return "secret_missing_or_revoked"
	default:
		return "secret_unavailable"
	}
}

// reasonOf 给非 secret 错误一个不含路径的短原因。
func reasonOf(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "not_found"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission_denied"
	}
	return "unavailable"
}

// DiscardSecrets 尽快清零并丢弃明文（缩短明文在内存中的存活期）。
// v0.1 的 pluginhost 还没有把实例配置送进插件进程的通道，因此 Edge 侧解析
// 只用于「双校验 + fail-closed」，解析结果立即销毁，绝不落盘、绝不进上报。
func DiscardSecrets(values map[string][]byte) {
	for k, v := range values {
		for i := range v {
			v[i] = 0
		}
		delete(values, k)
	}
}
