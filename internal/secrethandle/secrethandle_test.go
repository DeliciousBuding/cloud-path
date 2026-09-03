package secrethandle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSecretHandle(t *testing.T) {
	h, err := Parse("secret://api_token")
	if err != nil || h.Name() != "api_token" || h.String() != "secret://api_token" {
		t.Fatalf("handle=%+v err=%v", h, err)
	}
}

func TestSecretHandleRejectsTraversal(t *testing.T) {
	for _, raw := range []string{"api_token", "secret://", "secret://../token", "secret://a/b", `secret://a\b`, "secret://-bad", "secret://.hidden", "secret://a:b"} {
		if _, err := Parse(raw); !errors.Is(err, ErrInvalidHandle) {
			t.Fatalf("Parse(%q) err=%v, want ErrInvalidHandle", raw, err)
		}
	}
}

func TestUndeclaredSecretFailsClosed(t *testing.T) {
	h, err := Parse("secret://api_token")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDeclared(h, []string{"other"}); !errors.Is(err, ErrUndeclaredSecret) {
		t.Fatalf("err=%v, want ErrUndeclaredSecret", err)
	}
}

func TestFileProviderTenantInstanceIsolation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tenant-a", "instance-a")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "api_token"), []byte("value-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := FileProvider{Root: root}
	got, err := p.Resolve("tenant-a", "instance-a", "secret://api_token", []string{"api_token"})
	if err != nil || string(got) != "value-a" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := p.Resolve("tenant-b", "instance-a", "secret://api_token", []string{"api_token"}); err == nil {
		t.Fatal("tenant-b must not resolve tenant-a secret")
	}
}

func TestWithinRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside")
	if within(root, outside) {
		t.Fatal("outside path must not be within root")
	}
}

func TestFileProviderSizeLimit(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tenant-a", "instance-a")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api_token"), []byte(strings.Repeat("x", 17)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (FileProvider{Root: root, MaxBytes: 16}).Resolve("tenant-a", "instance-a", "secret://api_token", []string{"api_token"})
	if !errors.Is(err, ErrSecretTooLarge) {
		t.Fatalf("err=%v, want ErrSecretTooLarge", err)
	}
}
