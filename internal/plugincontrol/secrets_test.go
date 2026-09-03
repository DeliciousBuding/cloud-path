package plugincontrol_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

// writeSecret 在 Root/<tenant>/<instance>/<name> 落一个明文文件。
func writeSecret(t *testing.T, root, tenant, instance, name, value string) string {
	t.Helper()
	dir := filepath.Join(root, tenant, instance)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// secretsWithDeclared 构造一个 manifest 声明被注入的 provider（隔离文件系统依赖）。
func secretsWithDeclared(root string, declared map[string][]string) *plugincontrol.FileSecrets {
	f := &plugincontrol.FileSecrets{Root: root}
	f.Declared = func(pluginID string) ([]string, error) {
		list, ok := declared[pluginID]
		if !ok {
			return nil, os.ErrNotExist
		}
		return list, nil
	}
	return f
}

func TestBoundHandlesExtractsOnlyHandles(t *testing.T) {
	got := plugincontrol.BoundHandles(map[string]string{
		"api_key": "secret://api_key",
		"dup":     "secret://api_key",
		"plain":   "not-a-secret",
		"spaced":  "  secret://token  ",
		"empty":   "",
		"num":     "42",
	})
	want := []string{"secret://api_key", "secret://token"}
	if len(got) != len(want) {
		t.Fatalf("BoundHandles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("BoundHandles = %v, want %v（须排序去重）", got, want)
		}
	}
	if n := len(plugincontrol.BoundHandles(nil)); n != 0 {
		t.Fatalf("空配置应返回 0 个 handle，got %d", n)
	}
}

// TestResolveRequiresManifestDeclaration 是双校验的第一半：
// 实例绑定了 handle，但插件 manifest 没声明这个 secret 名 → fail-closed。
func TestResolveRequiresManifestDeclaration(t *testing.T) {
	root := t.TempDir()
	writeSecret(t, root, "tenant-a", "i1", "api_key", "PLAINTEXT-VALUE")
	f := secretsWithDeclared(root, map[string][]string{testPluginID: {"other_secret"}})

	_, err := f.Resolve("tenant-a", "i1", testPluginID, map[string]string{"k": "secret://api_key"})
	if !errors.Is(err, plugincontrol.ErrSecretForbidden) {
		t.Fatalf("未声明的 secret 必须 fail-closed，got %v", err)
	}
	if !strings.Contains(err.Error(), "secret_not_declared") {
		t.Fatalf("错误应带稳定原因码: %v", err)
	}
	if strings.Contains(err.Error(), "PLAINTEXT-VALUE") || strings.Contains(err.Error(), root) {
		t.Fatalf("错误文本不得含明文或本机绝对路径: %v", err)
	}
}

// TestResolveRequiresExplicitBinding 是双校验的第二半：
// manifest 声明了 secret，但实例配置没有显式绑定 → 一个字节都不读。
func TestResolveRequiresExplicitBinding(t *testing.T) {
	root := t.TempDir()
	writeSecret(t, root, "tenant-a", "i1", "api_key", "PLAINTEXT-VALUE")
	f := secretsWithDeclared(root, map[string][]string{testPluginID: {"api_key"}})

	got, err := f.Resolve("tenant-a", "i1", testPluginID, map[string]string{"mode": "fast", "n": "3"})
	if err != nil {
		t.Fatalf("未绑定 handle 不该报错: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("未显式绑定的 secret 绝不允许被解析: %v", got)
	}
}

func TestResolveHappyPath(t *testing.T) {
	root := t.TempDir()
	writeSecret(t, root, "tenant-a", "i1", "api_key", "PLAINTEXT-VALUE")
	writeSecret(t, root, "tenant-a", "i1", "token", "T0KEN")
	f := secretsWithDeclared(root, map[string][]string{testPluginID: {"api_key", "token"}})

	got, err := f.Resolve("tenant-a", "i1", testPluginID, map[string]string{
		"a": "secret://api_key", "b": "secret://token", "c": "plain",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got["secret://api_key"]) != "PLAINTEXT-VALUE" || string(got["secret://token"]) != "T0KEN" {
		t.Fatalf("解析结果错误: %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("只应解析显式绑定的 2 个 handle: %v", got)
	}
}

// TestResolveRevokedHandleNeverFallsBackToCache 是暗卷 #5：
// handle 被吊销（文件删除）后，插件重启/重新 reconcile 必须失败，
// 绝不从任何本地缓存恢复旧明文。
func TestResolveRevokedHandleNeverFallsBackToCache(t *testing.T) {
	root := t.TempDir()
	path := writeSecret(t, root, "tenant-a", "i1", "api_key", "PLAINTEXT-VALUE")
	f := secretsWithDeclared(root, map[string][]string{testPluginID: {"api_key"}})
	cfg := map[string]string{"k": "secret://api_key"}

	first, err := f.Resolve("tenant-a", "i1", testPluginID, cfg)
	if err != nil || string(first["secret://api_key"]) != "PLAINTEXT-VALUE" {
		t.Fatalf("首次解析应成功: %v %v", first, err)
	}
	plugincontrol.DiscardSecrets(first)

	// 吊销 = 删除本地明文文件。
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		got, err := f.Resolve("tenant-a", "i1", testPluginID, cfg)
		if !errors.Is(err, plugincontrol.ErrSecretForbidden) {
			t.Fatalf("吊销后第 %d 次解析必须 fail-closed，got %v (%v)", attempt, got, err)
		}
		if !strings.Contains(err.Error(), "secret_missing_or_revoked") {
			t.Fatalf("错误应带稳定原因码: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("吊销后不得返回任何明文: %v", got)
		}
		for _, v := range got {
			if strings.Contains(string(v), "PLAINTEXT-VALUE") {
				t.Fatal("吊销后回落了旧明文缓存")
			}
		}
	}
}

func TestResolveTenantAndInstanceIsolation(t *testing.T) {
	root := t.TempDir()
	writeSecret(t, root, "tenant-a", "i1", "api_key", "A-SECRET")
	writeSecret(t, root, "tenant-b", "i1", "api_key", "B-SECRET")
	f := secretsWithDeclared(root, map[string][]string{testPluginID: {"api_key"}})
	cfg := map[string]string{"k": "secret://api_key"}

	gotA, err := f.Resolve("tenant-a", "i1", testPluginID, cfg)
	if err != nil || string(gotA["secret://api_key"]) != "A-SECRET" {
		t.Fatalf("tenant-a 解析错误: %v %v", gotA, err)
	}
	// 另一个租户的同名实例读到的是自己的明文，绝不是 A 的。
	gotB, err := f.Resolve("tenant-b", "i1", testPluginID, cfg)
	if err != nil || string(gotB["secret://api_key"]) != "B-SECRET" {
		t.Fatalf("tenant-b 解析错误: %v %v", gotB, err)
	}
	// 未配置的实例 = 不存在 = fail-closed。
	if _, err := f.Resolve("tenant-a", "i2", testPluginID, cfg); !errors.Is(err, plugincontrol.ErrSecretForbidden) {
		t.Fatalf("其他实例不得读到 i1 的明文，got %v", err)
	}
}

func TestResolveRejectsTraversalAndBadHandles(t *testing.T) {
	root := t.TempDir()
	f := secretsWithDeclared(root, map[string][]string{testPluginID: {"api_key", "..", "x"}})
	bad := []struct{ tenant, instance, handle string }{
		{"tenant-a", "i1", "secret://../../etc/passwd"},
		{"tenant-a", "i1", "secret://"},
		{"tenant-a", "..", "secret://api_key"},
		{"../tenant-b", "i1", "secret://api_key"},
	}
	// 非 handle 形态的配置值是普通非敏感标量，不参与 secret 解析。
	if got, err := f.Resolve("tenant-a", "i1", testPluginID, map[string]string{"k": "not-a-handle"}); err != nil || len(got) != 0 {
		t.Fatalf("普通标量不该被当成 secret: %v %v", got, err)
	}
	for _, tc := range bad {
		_, err := f.Resolve(tc.tenant, tc.instance, testPluginID, map[string]string{"k": tc.handle})
		if !errors.Is(err, plugincontrol.ErrSecretForbidden) {
			t.Errorf("tenant=%q instance=%q handle=%q 必须 fail-closed，got %v", tc.tenant, tc.instance, tc.handle, err)
		}
		if err != nil && (strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "PLAINTEXT")) {
			t.Errorf("错误文本泄漏路径/明文: %v", err)
		}
	}
}

func TestResolveInsecurePermissionFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 权限位在 Windows 上不适用；secrethandle 已有平台分支")
	}
	root := t.TempDir()
	path := writeSecret(t, root, "tenant-a", "i1", "api_key", "PLAINTEXT-VALUE")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	f := secretsWithDeclared(root, map[string][]string{testPluginID: {"api_key"}})
	_, err := f.Resolve("tenant-a", "i1", testPluginID, map[string]string{"k": "secret://api_key"})
	if !errors.Is(err, plugincontrol.ErrSecretForbidden) {
		t.Fatalf("组/其他可读的 secret 文件必须 fail-closed，got %v", err)
	}
	if !strings.Contains(err.Error(), "secret_insecure_permission") {
		t.Fatalf("错误应带稳定原因码: %v", err)
	}
}

func TestResolveRequiresRootAndDeclarationSource(t *testing.T) {
	f := &plugincontrol.FileSecrets{}
	_, err := f.Resolve("tenant-a", "i1", testPluginID, map[string]string{"k": "secret://api_key"})
	if !errors.Is(err, plugincontrol.ErrSecretForbidden) {
		t.Fatalf("未配置 secret_dir 必须 fail-closed，got %v", err)
	}
	// 声明源不可用（插件未安装）同样 fail-closed，不得放行。
	f2 := plugincontrol.NewFileSecrets(t.TempDir(), filepath.Join(t.TempDir(), "missing-plugins"))
	if _, err := f2.Resolve("tenant-a", "i1", testPluginID, map[string]string{"k": "secret://api_key"}); !errors.Is(err, plugincontrol.ErrSecretForbidden) {
		t.Fatalf("manifest 不可读必须 fail-closed，got %v", err)
	}
}

// TestDeclaredFromInstalledManifest 用真实 manifest 验证声明来源：
// permissions.secrets 是唯一合法的声明处，且只携带名字、永不携带值。
func TestDeclaredFromInstalledManifest(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	pluginDir := filepath.Join(pluginsDir, registry.SafePluginID(testPluginID))
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: ` + testPluginID + `
version: 0.1.0
protocol: 1
entrypoint: ./plugin
permissions:
  secrets: [api_key]
`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	secretRoot := filepath.Join(root, "secrets")
	writeSecret(t, secretRoot, "tenant-a", "i1", "api_key", "PLAINTEXT-VALUE")
	writeSecret(t, secretRoot, "tenant-a", "i1", "undeclared", "OTHER")

	f := plugincontrol.NewFileSecrets(secretRoot, pluginsDir)
	got, err := f.Resolve("tenant-a", "i1", testPluginID, map[string]string{"k": "secret://api_key"})
	if err != nil || string(got["secret://api_key"]) != "PLAINTEXT-VALUE" {
		t.Fatalf("manifest 已声明的 handle 应解析成功: %v %v", got, err)
	}
	if _, err := f.Resolve("tenant-a", "i1", testPluginID, map[string]string{"k": "secret://undeclared"}); !errors.Is(err, plugincontrol.ErrSecretForbidden) {
		t.Fatalf("manifest 未声明的 handle 必须 fail-closed，got %v", err)
	}
}

func TestDiscardSecretsZeroesAndClears(t *testing.T) {
	buf := []byte("PLAINTEXT-VALUE")
	values := map[string][]byte{"secret://api_key": buf}
	plugincontrol.DiscardSecrets(values)
	if len(values) != 0 {
		t.Fatalf("明文映射应被清空: %v", values)
	}
	for _, b := range buf {
		if b != 0 {
			t.Fatal("明文缓冲区未被清零（缩短了明文在内存中的存活期才算达标）")
		}
	}
}

// ---- Applier（*Host）：期望态收敛 + 实际态观测 ----

// retired 读取替身 Manager 记录的停用/移除调用。
func (f *fakeHostManager) retired() (disabled, removed []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.disabled...), append([]string(nil), f.removed...)
}

// observingManager 是带实际态观测能力的替身（*pluginhost.Manager 的真实形状）。
type observingManager struct {
	fakeHostManager
	snapshots []pluginhost.InstanceSnapshot
	metrics   map[string]pluginhost.Metrics
}

func (m *observingManager) ListInstances(tenant string) []pluginhost.InstanceSnapshot {
	out := make([]pluginhost.InstanceSnapshot, 0, len(m.snapshots))
	for _, s := range m.snapshots {
		if s.Tenant == tenant {
			out = append(out, s)
		}
	}
	return out
}

func (m *observingManager) Metrics(tenant, id string) (pluginhost.Metrics, error) {
	if m.metrics == nil {
		return pluginhost.Metrics{}, os.ErrNotExist
	}
	return m.metrics[tenant+"/"+id], nil
}

func newApplierHost(t *testing.T, manager plugincontrol.HostManager, secrets plugincontrol.SecretResolver) (*plugincontrol.Host, *plugincontrol.Store, string, string) {
	t.Helper()
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	store := plugincontrol.NewStore(filepath.Join(root, "state"))
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", nil, nil)
	host, err := plugincontrol.NewHost(plugincontrol.HostOptions{
		Manager: manager, Store: store, PluginsDir: pluginsDir, LockPath: lockPath, Secrets: secrets,
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return host, store, pluginsDir, lockPath
}

func TestApplySnapshotConvergesManagerAndStore(t *testing.T) {
	manager := &fakeHostManager{}
	host, store, _, _ := newApplierHost(t, manager, nil)

	results, err := host.ApplySnapshot(context.Background(), "tenant-a", []api.PluginDesiredInstanceData{
		{InstanceID: "up", PluginID: testPluginID, Version: "0.1.0", Enabled: true, Isolation: plugincontrol.IsolationShared, Config: map[string]string{"mode": "fast"}},
		{InstanceID: "down", PluginID: testPluginID, Version: "0.1.0", Enabled: false, Isolation: plugincontrol.IsolationShared},
	})
	if err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("必须逐实例回报: %+v", results)
	}
	for _, r := range results {
		if r.Status != api.PluginAckApplied {
			t.Fatalf("实例 %s 未应用: %+v", r.InstanceID, r)
		}
	}
	_, created, started, _ := manager.snapshot()
	if len(created) != 1 || created[0].ID != "up" {
		t.Fatalf("created = %+v, want 只有 enabled 实例", created)
	}
	if len(started) != 1 || started[0] != "tenant-a/up" {
		t.Fatalf("started = %v, want 只启动 enabled 实例", started)
	}
	if disabled, _ := manager.retired(); len(disabled) != 1 || disabled[0] != "tenant-a/down" {
		t.Fatalf("disabled = %v, want [tenant-a/down]（期望停用必须真的下发停用）", disabled)
	}
	if created[0].Config["mode"] != "fast" {
		t.Fatalf("非敏感配置必须传给实例: %+v", created[0].Config)
	}
	// 期望态必须落盘（离线/重启续跑），且配置里只有标量与 handle。
	state, err := store.Load("tenant-a", "up")
	if err != nil {
		t.Fatalf("desired state 未落盘: %v", err)
	}
	if state.Config["mode"] != "fast" || !state.Enabled {
		t.Fatalf("desired state = %+v", state)
	}
	down, err := store.Load("tenant-a", "down")
	if err != nil || down.Enabled {
		t.Fatalf("disabled 实例的期望态错误: %+v %v", down, err)
	}
}

// TestApplySnapshotRetiresAbsentInstances 验证 Server 删除的实例会被停用并移除，
// 且默认保留插件数据（purge 是 Server 侧显式高风险选项，Edge 不自作主张）。
func TestApplySnapshotRetiresAbsentInstances(t *testing.T) {
	manager := &fakeHostManager{}
	host, store, _, _ := newApplierHost(t, manager, nil)
	ctx := context.Background()

	if _, err := host.ApplySnapshot(ctx, "tenant-a", []api.PluginDesiredInstanceData{
		{InstanceID: "keep", PluginID: testPluginID, Version: "0.1.0", Enabled: true},
		{InstanceID: "gone", PluginID: testPluginID, Version: "0.1.0", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.ApplySnapshot(ctx, "tenant-a", []api.PluginDesiredInstanceData{
		{InstanceID: "keep", PluginID: testPluginID, Version: "0.1.0", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	disabled, removed := manager.retired()
	if len(disabled) != 1 || disabled[0] != "tenant-a/gone" {
		t.Fatalf("disabled = %v, want [tenant-a/gone]", disabled)
	}
	if len(removed) != 1 || removed[0] != "tenant-a/gone" {
		t.Fatalf("removed = %v, want [tenant-a/gone]", removed)
	}
	if _, err := store.Load("tenant-a", "gone"); !errors.Is(err, plugincontrol.ErrNotFound) {
		t.Fatalf("被删除实例的本地期望态应清掉，got %v", err)
	}
	if _, err := store.Load("tenant-a", "keep"); err != nil {
		t.Fatalf("仍在快照中的实例不得被动到: %v", err)
	}
}

// TestApplySnapshotDoesNotRetireFailedInstances 防止「应用失败」被误判成「已删除」：
// 失败实例必须保留上一个完整已应用状态，等下一份快照再收敛。
func TestApplySnapshotDoesNotRetireFailedInstances(t *testing.T) {
	manager := &startFailingManager{fakeHostManager: fakeHostManager{}, failID: "bad"}
	host, store, _, _ := newApplierHost(t, manager, nil)
	ctx := context.Background()

	results, err := host.ApplySnapshot(ctx, "tenant-a", []api.PluginDesiredInstanceData{
		{InstanceID: "ok", PluginID: testPluginID, Version: "0.1.0", Enabled: true},
		{InstanceID: "bad", PluginID: testPluginID, Version: "0.1.0", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, r := range results {
		byID[r.InstanceID] = r.Status
	}
	if byID["ok"] != api.PluginAckApplied || byID["bad"] != api.PluginAckFailed {
		t.Fatalf("逐实例结果错误: %v", byID)
	}
	if _, err := store.Load("tenant-a", "bad"); err != nil {
		t.Fatalf("失败实例的期望态不得被当成已删除清掉: %v", err)
	}
	if _, removed := manager.retired(); len(removed) != 0 {
		t.Fatalf("失败实例不得被移除: %v", removed)
	}
}

// startFailingManager 让指定实例的 Start 失败（其余正常）。
type startFailingManager struct {
	fakeHostManager
	failID string
}

func (m *startFailingManager) Start(tenant, id string) error {
	if id == m.failID {
		return errors.New("process exited during handshake")
	}
	return m.fakeHostManager.Start(tenant, id)
}

// TestApplySnapshotSecretFailClosed 验证 §7：绑定 handle 但解析失败 → 该实例
// fail-closed，且不落任何明文、不启动进程。
func TestApplySnapshotSecretFailClosed(t *testing.T) {
	manager := &fakeHostManager{}
	root := t.TempDir()
	secrets := secretsWithDeclared(filepath.Join(root, "secrets"), map[string][]string{testPluginID: {"other"}})
	host, store, _, _ := newApplierHost(t, manager, secrets)

	results, err := host.ApplySnapshot(context.Background(), "tenant-a", []api.PluginDesiredInstanceData{
		{InstanceID: "s1", PluginID: testPluginID, Version: "0.1.0", Enabled: true,
			Config: map[string]string{"api_key": "secret://api_key"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != api.PluginAckFailed {
		t.Fatalf("secret 不可解析必须整实例 fail-closed: %+v", results)
	}
	if !strings.Contains(results[0].Detail, plugincontrol.ErrSecretForbidden.Error()) {
		t.Fatalf("失败原因应可机读: %q", results[0].Detail)
	}
	if _, err := store.Load("tenant-a", "s1"); !errors.Is(err, plugincontrol.ErrNotFound) {
		t.Fatalf("fail-closed 的实例不得写入本地期望态: %v", err)
	}
	if _, _, started, _ := manager.snapshot(); len(started) != 0 {
		t.Fatalf("fail-closed 的实例不得启动: %v", started)
	}
}

// TestApplySnapshotNeverPersistsPlaintext 是暗卷 #5 的落盘半边：
// 即使解析成功，本地期望态文件里也只能出现 handle，绝不出现明文。
func TestApplySnapshotNeverPersistsPlaintext(t *testing.T) {
	manager := &fakeHostManager{}
	root := t.TempDir()
	secretRoot := filepath.Join(root, "secrets")
	writeSecret(t, secretRoot, "tenant-a", "s1", "api_key", "PLAINTEXT-VALUE")
	secrets := secretsWithDeclared(secretRoot, map[string][]string{testPluginID: {"api_key"}})
	host, store, _, _ := newApplierHost(t, manager, secrets)

	results, err := host.ApplySnapshot(context.Background(), "tenant-a", []api.PluginDesiredInstanceData{
		{InstanceID: "s1", PluginID: testPluginID, Version: "0.1.0", Enabled: true,
			Config: map[string]string{"api_key": "secret://api_key"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != api.PluginAckApplied {
		t.Fatalf("声明且绑定齐全的 handle 应成功: %+v", results)
	}
	// 扫描本地期望态目录（不含 secret provider 自己的存储）：任何文件都不得含明文。
	walk(t, store.Dir, func(path string, data []byte) {
		if strings.Contains(string(data), "PLAINTEXT-VALUE") {
			t.Errorf("明文落盘到 %s", path)
		}
	})
}

func walk(t *testing.T, root string, fn func(path string, data []byte)) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		fn(path, data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplySnapshotRejectsInvalidInstances(t *testing.T) {
	manager := &fakeHostManager{}
	host, _, _, _ := newApplierHost(t, manager, nil)
	results, err := host.ApplySnapshot(context.Background(), "tenant-a", []api.PluginDesiredInstanceData{
		{InstanceID: "", PluginID: testPluginID, Version: "0.1.0", Enabled: true},
		{InstanceID: "no-version", PluginID: testPluginID, Version: " ", Enabled: true},
		{InstanceID: "../escape", PluginID: testPluginID, Version: "0.1.0", Enabled: true},
		{InstanceID: "bad-iso", PluginID: testPluginID, Version: "0.1.0", Enabled: true, Isolation: "wat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("必须逐实例回报: %+v", results)
	}
	for _, r := range results {
		if r.Status != api.PluginAckFailed {
			t.Errorf("非法实例 %q 必须 failed: %+v", r.InstanceID, r)
		}
		if strings.TrimSpace(r.Detail) == "" {
			t.Errorf("非法实例 %q 必须带原因", r.InstanceID)
		}
	}
	if _, created, started, _ := manager.snapshot(); len(created) != 0 || len(started) != 0 {
		t.Fatalf("非法实例不得进入 Manager: created=%+v started=%v", created, started)
	}
}

// TestObserveReportsInstallationsWithoutLocalPaths 锁定上报红线：
// 安装物公开事实里不得出现本机绝对路径、启动参数或环境变量。
func TestObserveReportsInstallationsWithoutLocalPaths(t *testing.T) {
	manager := &fakeHostManager{}
	host, _, pluginsDir, _ := newApplierHost(t, manager, nil)
	installations, _, err := host.Observe(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(installations) != 1 {
		t.Fatalf("installations = %+v, want 1", installations)
	}
	row := installations[0]
	if row.PluginID != testPluginID || row.Version != "0.1.0" || row.Kind != "Driver" || row.Protocol != 1 {
		t.Fatalf("installations[0] = %+v", row)
	}
	if !row.Verified || row.Digest == "" {
		t.Fatalf("信任事实缺失: %+v", row)
	}
	blob, err := json.Marshal(installations)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{pluginsDir, "plugin.yaml", "assets"} {
		if strings.Contains(string(blob), leak) {
			t.Errorf("installations 泄漏本机路径片段 %q: %s", leak, blob)
		}
	}
}

// TestObserveWithoutObserverStaysHonest 锁定 desired/observed 分离：
// Manager 不支持观测时回报 HostOnline=false + STOPPED/UNKNOWN，
// 绝不把「期望启用」渲染成「实际健康」。
func TestObserveWithoutObserverStaysHonest(t *testing.T) {
	manager := &fakeHostManager{}
	host, _, _, _ := newApplierHost(t, manager, nil)
	if _, err := host.ApplySnapshot(context.Background(), "tenant-a", []api.PluginDesiredInstanceData{
		{InstanceID: "up", PluginID: testPluginID, Version: "0.1.0", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	_, observed, err := host.Observe(context.Background(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 {
		t.Fatalf("observed = %+v, want 1", observed)
	}
	row := observed[0]
	if row.HostOnline {
		t.Fatal("不可观测时不得声称 host online")
	}
	if row.State != pluginhost.StateStopped.String() || row.Health != pluginhost.HealthUnknown.String() {
		t.Fatalf("不可观测时应回报 STOPPED/UNKNOWN，got %s/%s", row.State, row.Health)
	}
	if row.Detail == "" {
		t.Fatal("应说明为何不可观测")
	}
	// 别的租户看不到本租户实例。
	if _, other, _ := host.Observe(context.Background(), "tenant-b"); len(other) != 0 {
		t.Fatalf("跨租户泄漏: %+v", other)
	}
}

// TestObserveReportsRealInstanceState 验证有观测能力时回报真实运行态，
// 且 detail 只由计数事实组成（绝不含 stdout/stderr 原文）。
func TestObserveReportsRealInstanceState(t *testing.T) {
	manager := &observingManager{snapshots: []pluginhost.InstanceSnapshot{{
		Tenant: "tenant-a", InstanceID: "up", PluginID: testPluginID, Version: "0.1.0",
		Enabled: true, State: pluginhost.StateHealthy, Health: pluginhost.HealthHealthy,
		Restarts: 2, Crashes: 1, ConsecutiveFailures: 0, Launches: 3,
		Config: map[string]string{"api_key": "secret://api_key"},
	}}, metrics: map[string]pluginhost.Metrics{
		"tenant-a/up": {MessageRate: 1.5, RestartCount: 2},
	}}
	host, _, _, _ := newApplierHost(t, manager, nil)
	_, observed, err := host.Observe(context.Background(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 {
		t.Fatalf("observed = %+v", observed)
	}
	row := observed[0]
	if !row.HostOnline || row.State != "HEALTHY" || row.RestartCount != 2 {
		t.Fatalf("observed[0] = %+v", row)
	}
	if row.MessageRate != 1.5 {
		t.Fatalf("message_rate = %v, want 1.5", row.MessageRate)
	}
	for _, want := range []string{"restarts=2", "crashes=1", "launches=3"} {
		if !strings.Contains(row.Detail, want) {
			t.Errorf("detail 缺少计数事实 %q: %q", want, row.Detail)
		}
	}
	// 实例配置（可能含 handle）绝不出现在上报里。
	blob, _ := json.Marshal(observed)
	if strings.Contains(string(blob), "api_key") || strings.Contains(string(blob), "secret://") {
		t.Fatalf("observed 不得携带实例配置: %s", blob)
	}
}
