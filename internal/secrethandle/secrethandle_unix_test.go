//go:build !windows

package secrethandle

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileProviderRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "value")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "tenant-a", "instance-a")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "api_token")); err != nil {
		t.Fatal(err)
	}
	_, err := (FileProvider{Root: root}).Resolve("tenant-a", "instance-a", "secret://api_token", []string{"api_token"})
	if !errors.Is(err, ErrUnsafeSecretPath) {
		t.Fatalf("err=%v, want ErrUnsafeSecretPath", err)
	}
}
