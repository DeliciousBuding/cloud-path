package registry

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Manifest is the machine-readable root plugin.yaml contract. The field names
// follow spec/plugin-manifest.schema.json exactly.
type Manifest struct {
	APIVersion    string           `yaml:"apiVersion" json:"apiVersion"`
	Kind          string           `yaml:"kind" json:"kind"`
	ID            string           `yaml:"id" json:"id"`
	Version       string           `yaml:"version" json:"version"`
	Protocol      int              `yaml:"protocol" json:"protocol"`
	Entrypoint    string           `yaml:"entrypoint" json:"entrypoint"`
	Compatibility Compatibility    `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
	Permissions   Permissions      `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	Capabilities  []string         `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Requirements  []map[string]any `yaml:"requirements,omitempty" json:"requirements,omitempty"`
}

// Compatibility describes Core version compatibility.
type Compatibility struct {
	Core string `yaml:"core,omitempty" json:"core,omitempty"`
}

// Permissions is the permission disclosure block.
type Permissions struct {
	Hardware   []string `yaml:"hardware,omitempty" json:"hardware,omitempty"`
	Network    []string `yaml:"network,omitempty" json:"network,omitempty"`
	Filesystem []string `yaml:"filesystem,omitempty" json:"filesystem,omitempty"`
	Secrets    []string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// ParseManifest decodes manifest data without schema validation.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	return &m, nil
}

// LoadManifest reads a manifest file without schema validation.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin.yaml %s: %w", path, err)
	}
	return ParseManifest(data)
}

// ValidateManifest validates YAML manifest data against a JSON Schema document
// and returns the typed manifest on success.
func ValidateManifest(data []byte, schema []byte) (*Manifest, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	validator, err := NewSchemaValidator(schema)
	if err != nil {
		return nil, fmt.Errorf("load manifest schema: %w", err)
	}
	if err := validator.Validate(raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ValidateManifestFile reads and validates a manifest file.
func ValidateManifestFile(path, schemaPath string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin.yaml %s: %w", path, err)
	}
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest schema %s: %w", schemaPath, err)
	}
	return ValidateManifest(data, schema)
}

// PermissionSummary returns a compact human-readable permission disclosure.
func (m *Manifest) PermissionSummary() string {
	p := m.Permissions
	out := ""
	if len(p.Hardware) > 0 {
		out += " hardware=" + joinNonEmpty(p.Hardware)
	}
	if len(p.Network) > 0 {
		out += " network=" + joinNonEmpty(p.Network)
	}
	if len(p.Filesystem) > 0 {
		out += " filesystem=" + joinNonEmpty(p.Filesystem)
	}
	if len(p.Secrets) > 0 {
		out += " secrets=" + joinNonEmpty(p.Secrets)
	}
	if out == "" {
		return "none"
	}
	return out[1:]
}

func joinNonEmpty(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
