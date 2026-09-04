package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 发布的二进制必须在没有仓库 checkout 的机器上也能校验 manifest：
// schema 文件缺失时回落到内嵌副本，而不是让 install 直接失败。
func TestLoadManifestSchemaFallsBackToEmbedded(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-schema.json")
	data, source, err := LoadManifestSchema(missing)
	if err != nil {
		t.Fatalf("embedded fallback failed: %v", err)
	}
	if source != "embedded" {
		t.Fatalf("source = %q, want embedded", source)
	}
	if _, err := NewSchemaValidator(data); err != nil {
		t.Fatalf("embedded schema is not a usable JSON Schema: %v", err)
	}
	if !strings.Contains(string(data), "plugin-manifest") && !strings.Contains(string(data), "apiVersion") {
		t.Fatal("embedded schema does not look like the plugin manifest schema")
	}
}

// 空路径同样走内嵌副本（CLI 默认值被显式清空时不能崩）。
func TestLoadManifestSchemaEmptyPathUsesEmbedded(t *testing.T) {
	if _, source, err := LoadManifestSchema(""); err != nil || source != "embedded" {
		t.Fatalf("source=%q err=%v, want embedded/nil", source, err)
	}
}

// 显式文件优先：运维可以用自己的 schema 覆盖内嵌副本。
func TestLoadManifestSchemaPrefersFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.json")
	if err := os.WriteFile(path, []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data, source, err := LoadManifestSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	if source != "file:"+path || string(data) != `{"type":"object"}` {
		t.Fatalf("source=%q data=%q", source, data)
	}
}

// 非「不存在」的读取错误必须失败，不得静默降级到内嵌副本。
func TestLoadManifestSchemaSurfacesReadErrors(t *testing.T) {
	dir := t.TempDir()
	// 目录当文件读：Windows/Linux 都不是 ErrNotExist，必须报错。
	if _, _, err := LoadManifestSchema(dir); err == nil {
		t.Fatal("expected error when schema path is a directory")
	}
}

// 内嵌副本必须与仓库 SSOT 逐字节一致，防止出现第二份漂移的 schema。
func TestEmbeddedSchemaMatchesRepoSSOT(t *testing.T) {
	repo := filepath.Join("..", "..", "spec", "plugin-manifest.schema.json")
	want, err := os.ReadFile(repo)
	if err != nil {
		t.Skipf("repo schema unavailable: %v", err)
	}
	got, _, err := LoadManifestSchema(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("embedded schema drifted from spec/plugin-manifest.schema.json")
	}
}

// 端到端：schema 路径不存在时 install 仍必须成功，并用内嵌 schema 完成校验。
// 对应真实验收场景——干净机器上只有发布的二进制，没有仓库 checkout。
func TestInstallWorksWithoutRepoSchemaFile(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	inst.SchemaPath = filepath.Join(t.TempDir(), "absent-schema.json")
	res, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
	})
	if err != nil {
		t.Fatalf("install without repo schema file: %v", err)
	}
	if res.SchemaSource != "embedded" {
		t.Fatalf("SchemaSource = %q, want embedded", res.SchemaSource)
	}
	if !res.Verified || res.Mode != TrustModeExplicitDigest {
		t.Fatalf("mode=%q verified=%v, want explicit-digest true", res.Mode, res.Verified)
	}
}

// 反向断言：回落到内嵌 schema 不等于跳过校验——非法 manifest 仍必须 fail closed。
func TestEmbeddedSchemaStillRejectsInvalidManifest(t *testing.T) {
	manifest := []byte("apiVersion: not-a-valid-api-version\nkind: Driver\n")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	inst.SchemaPath = filepath.Join(t.TempDir(), "absent-schema.json")
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
	})
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("want ErrInvalidManifest with embedded schema, got %v", err)
	}
	if _, err := os.Stat(pluginsDir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "plugins.lock" && strings.HasPrefix(e.Name(), "not-a-valid") {
			t.Fatalf("rejected install left plugin dir %q", e.Name())
		}
	}
}
