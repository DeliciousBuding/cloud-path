package registry

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

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
	Contributes   *Contributes     `yaml:"contributes,omitempty" json:"contributes,omitempty"`
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

// Contributes is the typed manifest contributions block. A plugin declares the
// drivers / applications / connectors it provides; each contribution has a
// stable id that is distinct from the plugin id, and a single plugin may
// contribute multiple entries of its own kind.
type Contributes struct {
	Drivers      []DriverContribution      `yaml:"drivers,omitempty" json:"drivers,omitempty"`
	Applications []ApplicationContribution `yaml:"applications,omitempty" json:"applications,omitempty"`
	Connectors   []ConnectorContribution   `yaml:"connectors,omitempty" json:"connectors,omitempty"`
}

// DriverContribution is one driver provided by a Driver plugin.
type DriverContribution struct {
	ID                string `yaml:"id" json:"id"`
	Title             string `yaml:"title,omitempty" json:"title,omitempty"`
	Descriptor        string `yaml:"descriptor,omitempty" json:"descriptor,omitempty"`
	ConfigSchema      string `yaml:"configSchema,omitempty" json:"configSchema,omitempty"`
	Discovery         string `yaml:"discovery,omitempty" json:"discovery,omitempty"`
	CapabilityCatalog string `yaml:"capabilityCatalog,omitempty" json:"capabilityCatalog,omitempty"`
}

// ApplicationContribution is one application provided by an Application plugin.
type ApplicationContribution struct {
	ID           string           `yaml:"id" json:"id"`
	Title        string           `yaml:"title,omitempty" json:"title,omitempty"`
	Requirements []map[string]any `yaml:"requirements,omitempty" json:"requirements,omitempty"`
}

// ConnectorContribution is one connector provided by a Connector plugin.
type ConnectorContribution struct {
	ID        string `yaml:"id" json:"id"`
	Title     string `yaml:"title,omitempty" json:"title,omitempty"`
	Direction string `yaml:"direction,omitempty" json:"direction,omitempty"`
	Host      string `yaml:"host,omitempty" json:"host,omitempty"`
}

// ParseManifest decodes manifest data without schema validation.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	return &m, nil
}

// ReadManifest reads and parses a plugin.yaml file without schema or semantic
// validation. It is the untyped read path; callers that need the full contract
// should use ValidateManifest / ValidateManifestFile.
func ReadManifest(path string) (*Manifest, error) {
	data, err := readFileRetry(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin.yaml %s: %w", path, err)
	}
	return ParseManifest(data)
}

// LoadManifest reads a manifest file without schema validation.
func LoadManifest(path string) (*Manifest, error) {
	return ReadManifest(path)
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
	if err := ValidateContributions(m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	return m, nil
}

// ValidateManifestFile reads and validates a manifest file.
func ValidateManifestFile(path, schemaPath string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin.yaml %s: %w", path, err)
	}
	schema, _, err := LoadManifestSchema(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest schema %s: %w", schemaPath, err)
	}
	return ValidateManifest(data, schema)
}

// PermissionExpansion returns the permission entries present in incoming but not
// in existing, formatted as category:value, in deterministic order. It returns
// nil when incoming is a subset of or equal to existing, i.e. no expansion.
func PermissionExpansion(existing, incoming *Permissions) []string {
	if existing == nil {
		existing = &Permissions{}
	}
	if incoming == nil {
		incoming = &Permissions{}
	}
	seen := make(map[string]bool)
	for _, v := range existing.Hardware {
		seen["hardware\x00"+v] = true
	}
	for _, v := range existing.Network {
		seen["network\x00"+v] = true
	}
	for _, v := range existing.Filesystem {
		seen["filesystem\x00"+v] = true
	}
	for _, v := range existing.Secrets {
		seen["secrets\x00"+v] = true
	}
	var added []string
	add := func(category string, values []string) {
		for _, v := range values {
			key := category + "\x00" + v
			if !seen[key] {
				added = append(added, category+":"+v)
			}
		}
	}
	add("hardware", incoming.Hardware)
	add("network", incoming.Network)
	add("filesystem", incoming.Filesystem)
	add("secrets", incoming.Secrets)
	sort.Strings(added)
	return added
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

// ValidateContributions enforces the typed contributes contract:
//   - every contribution id is stable and filesystem-safe: non-empty, no path
//     separators, no "..", no whitespace and no control characters;
//   - contribution ids are unique across the whole plugin;
//   - contributes match the plugin kind (a Driver plugin must contribute
//     drivers, never applications or connectors, and so on).
//
// A plugin that declares no contributes block is unchanged and remains valid,
// so legacy manifests keep working.
func ValidateContributions(m *Manifest) error {
	if m == nil {
		return nil
	}
	c := m.Contributes
	if c == nil {
		return nil
	}
	seen := map[string]bool{}
	for i := range c.Drivers {
		if err := validateContributionID("drivers", i, c.Drivers[i].ID, seen); err != nil {
			return err
		}
	}
	for i := range c.Applications {
		if err := validateContributionID("applications", i, c.Applications[i].ID, seen); err != nil {
			return err
		}
	}
	for i := range c.Connectors {
		if err := validateContributionID("connectors", i, c.Connectors[i].ID, seen); err != nil {
			return err
		}
	}

	switch m.Kind {
	case "Driver":
		if len(c.Applications) > 0 || len(c.Connectors) > 0 {
			return fmt.Errorf("Driver plugin must contribute drivers, not applications or connectors")
		}
	case "Application":
		if len(c.Drivers) > 0 || len(c.Connectors) > 0 {
			return fmt.Errorf("Application plugin must contribute applications, not drivers or connectors")
		}
	case "Connector":
		if len(c.Drivers) > 0 || len(c.Applications) > 0 {
			return fmt.Errorf("Connector plugin must contribute connectors, not drivers or applications")
		}
	default:
		return fmt.Errorf("unknown manifest kind %q for contributes", m.Kind)
	}
	return nil
}

func validateContributionID(category string, index int, id string, seen map[string]bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%s[%d]: contribution id is empty", category, index)
	}
	if strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("%s[%d]: contribution id %q contains a path separator", category, index, id)
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("%s[%d]: contribution id %q must not contain \"..\"", category, index, id)
	}
	for _, r := range id {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("%s[%d]: contribution id %q contains whitespace or a control character", category, index, id)
		}
	}
	if seen[id] {
		return fmt.Errorf("%s[%d]: duplicate contribution id %q", category, index, id)
	}
	seen[id] = true
	return nil
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
