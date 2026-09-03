package driverkit_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit/sampleexternal"
)

// internalImportPrefix 是拆仓红线：外部插件源码不得出现该前缀。
const internalImportPrefix = "github.com/DeliciousBuding/cloud-path/internal/"

// repoRoot 由当前测试文件位置推导仓库根（sdk/go/driverkit → 仓库根）。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// findInternalImports 扫描 dir 下所有 *.go 文件的 import，返回命中的 internal 导入路径。
func findInternalImports(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var hits []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, internalImportPrefix) {
				hits = append(hits, p)
			}
		}
	}
	return hits, nil
}

// TestSTCBHasNoInternalImports 锁定 examples/stcb 的非测试与测试源码都不 import internal/*。
func TestSTCBHasNoInternalImports(t *testing.T) {
	hits, err := findInternalImports(filepath.Join(repoRoot(t), "examples", "stcb"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("examples/stcb 仍依赖 internal/*: %v", hits)
	}
}

// TestInternalImportScannerDetectsViolation 反向验证：contract fixture 含 internal import 时扫描器必须报错。
func TestInternalImportScannerDetectsViolation(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "sdk", "go", "driverkit", "testdata", "contractfixture")
	hits, err := findInternalImports(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := internalImportPrefix + "model"
	for _, h := range hits {
		if h == want {
			return
		}
	}
	t.Fatalf("扫描器应命中 %q，got %v", want, hits)
}

// TestExternalStyleDriverCompiles 用仓外风格 sample 证明：外部 Driver 只依赖公共 SDK
// 即可实现 Adapter / DescriptorProvider / CapabilityProvider。本测试 import sample 包，
// 即完成一次编译证明；go build ./... 也会单独编译该 sample 包。
func TestExternalStyleDriverCompiles(t *testing.T) {
	var d driverkit.Adapter = sampleexternal.Driver{}
	if d.Name() != "sample.external" {
		t.Fatalf("Name() = %q", d.Name())
	}
	if got := d.SupportedCommands(); len(got) != 1 || got[0] != "ping" {
		t.Fatalf("SupportedCommands() = %v", got)
	}

	hits, err := findInternalImports(filepath.Join(repoRoot(t), "sdk", "go", "driverkit", "sampleexternal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("sampleexternal 不应 import internal/*: %v", hits)
	}
}
