package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LockFormatVersion is the current plugins.lock schema version.
const LockFormatVersion = 1

// LockedPlugin pins one installed plugin to an exact release and digest.
type LockedPlugin struct {
	ID                string `yaml:"id" json:"id"`
	Version           string `yaml:"version" json:"version"`
	Digest            string `yaml:"digest" json:"digest"`
	Source            string `yaml:"source" json:"source"`
	Verified          bool   `yaml:"verified" json:"verified"`
	VerifiedPublisher string `yaml:"verifiedPublisher,omitempty" json:"verifiedPublisher,omitempty"`
	Protocol          int    `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	Compatibility     string `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
}

// LockFile is the local, machine-readable installation record.
type LockFile struct {
	FormatVersion int            `yaml:"format_version" json:"formatVersion"`
	Plugins       []LockedPlugin `yaml:"plugins" json:"plugins"`
}

// NewLockFile returns an empty lockfile with the current format version.
func NewLockFile() *LockFile {
	return &LockFile{FormatVersion: LockFormatVersion}
}

// LoadLockFile reads a lockfile. A missing file is treated as an empty lock.
func LoadLockFile(path string) (*LockFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewLockFile(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugins.lock %s: %w", path, err)
	}
	var lock LockFile
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parse plugins.lock %s: %w", path, err)
	}
	if lock.FormatVersion == 0 {
		lock.FormatVersion = LockFormatVersion
	}
	return &lock, nil
}

// WriteLockFile writes the lockfile atomically.
func WriteLockFile(path string, lock *LockFile) error {
	if lock == nil {
		return errors.New("lockfile is nil")
	}
	if lock.FormatVersion == 0 {
		lock.FormatVersion = LockFormatVersion
	}
	data, err := yaml.Marshal(lock)
	if err != nil {
		return fmt.Errorf("marshal plugins.lock: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create lock dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".plugins.lock-*")
	if err != nil {
		return fmt.Errorf("create temporary plugins.lock: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary plugins.lock: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary plugins.lock: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace plugins.lock %s: %w", path, err)
	}
	return nil
}

// Find returns a locked plugin by id.
func (l *LockFile) Find(id string) (*LockedPlugin, bool) {
	if l == nil {
		return nil, false
	}
	for i := range l.Plugins {
		if l.Plugins[i].ID == id {
			return &l.Plugins[i], true
		}
	}
	return nil, false
}

// Upsert adds or replaces a lock entry by id.
func (l *LockFile) Upsert(entry LockedPlugin) {
	if l == nil {
		return
	}
	for i := range l.Plugins {
		if l.Plugins[i].ID == entry.ID {
			l.Plugins[i] = entry
			return
		}
	}
	l.Plugins = append(l.Plugins, entry)
}

// Remove deletes a lock entry by id.
func (l *LockFile) Remove(id string) bool {
	if l == nil {
		return false
	}
	for i := range l.Plugins {
		if l.Plugins[i].ID == id {
			l.Plugins = append(l.Plugins[:i], l.Plugins[i+1:]...)
			return true
		}
	}
	return false
}
