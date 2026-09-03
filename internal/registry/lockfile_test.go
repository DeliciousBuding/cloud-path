package registry

import (
	"path/filepath"
	"testing"
)

func TestLockfileRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.lock")
	original := &LockFile{
		FormatVersion: LockFormatVersion,
		Plugins: []LockedPlugin{{
			ID:            "io.github.example.driver",
			Version:       "0.1.0",
			Digest:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Source:        "https://github.com/example/driver",
			Verified:      true,
			Protocol:      1,
			Compatibility: ">=0.1.0 <0.2.0",
		}},
	}
	if err := WriteLockFile(path, original); err != nil {
		t.Fatalf("WriteLockFile: %v", err)
	}
	got, err := LoadLockFile(path)
	if err != nil {
		t.Fatalf("LoadLockFile: %v", err)
	}
	if got.FormatVersion != LockFormatVersion || len(got.Plugins) != 1 {
		t.Fatalf("unexpected lock: %+v", got)
	}
	plugin := got.Plugins[0]
	if plugin.ID != original.Plugins[0].ID || plugin.Version != original.Plugins[0].Version ||
		plugin.Digest != original.Plugins[0].Digest || plugin.Source != original.Plugins[0].Source ||
		!plugin.Verified || plugin.Protocol != 1 || plugin.Compatibility != original.Plugins[0].Compatibility {
		t.Fatalf("roundtrip mismatch: %+v", plugin)
	}
}

func TestLockFileMissingIsEmpty(t *testing.T) {
	lock, err := LoadLockFile(filepath.Join(t.TempDir(), "missing.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if lock.FormatVersion != LockFormatVersion || len(lock.Plugins) != 0 {
		t.Fatalf("missing lock should be empty: %+v", lock)
	}
}
