package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// RegistryEntry is one reviewed plugin record. Field names are fixed by
// docs/architecture/registry.md and stay stable in lock/index files.
type RegistryEntry struct {
	ID                string `yaml:"id" json:"id"`
	Version           string `yaml:"version" json:"version"`
	Kind              string `yaml:"kind" json:"kind"`
	Source            string `yaml:"source" json:"source"`
	Digest            string `yaml:"digest" json:"digest"`
	VerifiedPublisher string `yaml:"verifiedPublisher,omitempty" json:"verifiedPublisher,omitempty"`
	Protocol          int    `yaml:"protocol" json:"protocol"`
	Compatibility     string `yaml:"compatibility" json:"compatibility"`
}

// RegistryIndex is the in-memory form of a curated registry index.
type RegistryIndex struct {
	Version int             `yaml:"version" json:"version"`
	Plugins []RegistryEntry `yaml:"plugins" json:"plugins"`
}

// Find returns the registry entry for id.
func (idx *RegistryIndex) Find(id string) (*RegistryEntry, bool) {
	if idx == nil {
		return nil, false
	}
	for i := range idx.Plugins {
		if idx.Plugins[i].ID == id {
			return &idx.Plugins[i], true
		}
	}
	return nil, false
}

// LoadRegistryIndex reads and validates a curated registry index YAML file.
// Malformed entries are rejected before use so a corrupt index fails closed
// rather than silently supplying unverified metadata.
func LoadRegistryIndex(path string) (*RegistryIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry index %s: %w", path, err)
	}
	var idx RegistryIndex
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse registry index %s: %w", path, err)
	}
	for _, entry := range idx.Plugins {
		if err := ValidateRegistryEntry(entry); err != nil {
			return nil, fmt.Errorf("registry index %s: %w", path, err)
		}
	}
	return &idx, nil
}

// ValidateRegistryEntry enforces the fixed registry fields. A registry record
// is source-controlled metadata, so malformed entries are rejected before use.
func ValidateRegistryEntry(entry RegistryEntry) error {
	if strings.TrimSpace(entry.ID) == "" {
		return errors.New("registry entry: id is required")
	}
	if strings.TrimSpace(entry.Version) == "" {
		return errors.New("registry entry: version is required")
	}
	if !isManifestKind(entry.Kind) {
		return fmt.Errorf("registry entry: invalid kind %q (Driver|Application|Connector)", entry.Kind)
	}
	if strings.TrimSpace(entry.Source) == "" {
		return errors.New("registry entry: source is required")
	}
	if _, err := NormalizeDigest(entry.Digest); err != nil {
		return fmt.Errorf("registry entry: %w", err)
	}
	if entry.Protocol < 0 {
		return errors.New("registry entry: protocol must be non-negative")
	}
	if strings.TrimSpace(entry.Compatibility) == "" {
		return errors.New("registry entry: compatibility is required")
	}
	return nil
}

// EncodeRegistryEntry is a round-trip helper used by future registry readers.
func EncodeRegistryEntry(entry RegistryEntry) ([]byte, error) {
	if err := ValidateRegistryEntry(entry); err != nil {
		return nil, err
	}
	return json.MarshalIndent(entry, "", "  ")
}

func isManifestKind(kind string) bool {
	switch kind {
	case "Driver", "Application", "Connector":
		return true
	default:
		return false
	}
}
