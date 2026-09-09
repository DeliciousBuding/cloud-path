package registry

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	cloudpath "github.com/DeliciousBuding/cloud-path"
)

// PluginUI is the declarative UI contribution declared by an Application or
// Driver contribution. It is metadata only: no arbitrary script or local path
// is executable through this type.
type PluginUI struct {
	APIVersion int           `yaml:"apiVersion" json:"apiVersion"`
	Navigation *UINavigation `yaml:"navigation,omitempty" json:"navigation,omitempty"`
	Pages      []UIPage      `yaml:"pages,omitempty" json:"pages,omitempty"`
	Device     *UIDevice     `yaml:"device,omitempty" json:"device,omitempty"`
}

// UINavigation declares the stable business navigation entry for an Application.
type UINavigation struct {
	Title      string `yaml:"title" json:"title"`
	Icon       string `yaml:"icon,omitempty" json:"icon,omitempty"`
	Order      int    `yaml:"order,omitempty" json:"order,omitempty"`
	Route      string `yaml:"route" json:"route"`
	Visibility string `yaml:"visibility,omitempty" json:"visibility,omitempty"`
}

// UIPage is one declarative page under an Application route.
type UIPage struct {
	ID       string      `yaml:"id" json:"id"`
	Title    string      `yaml:"title" json:"title"`
	Sections []UISection `yaml:"sections" json:"sections"`
}

// UIDevice extends a Driver's device detail page.
type UIDevice struct {
	Sections []UISection `yaml:"sections,omitempty" json:"sections,omitempty"`
}

// UISection is a Core-rendered, allowlisted UI primitive.
type UISection struct {
	Type         string           `yaml:"type" json:"type"`
	Source       string           `yaml:"source,omitempty" json:"source,omitempty"`
	RecordType   string           `yaml:"recordType,omitempty" json:"recordType,omitempty"`
	Presentation string           `yaml:"presentation,omitempty" json:"presentation,omitempty"`
	Text         string           `yaml:"text,omitempty" json:"text,omitempty"`
	Entry        string           `yaml:"entry,omitempty" json:"entry,omitempty"`
	Scopes       []string         `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	Fields       []map[string]any `yaml:"fields,omitempty" json:"fields,omitempty"`
}

const (
	pluginUIMaxBytes      = 64 << 10
	pluginUIPathMaxBytes  = 240
	pluginUIPageMaxCount  = 8
	pluginUISectionMax    = 32
	pluginUIDeviceSection = 24
)

var pluginUISlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

var pluginUISectionSources = map[string]map[string]bool{
	"status":      {"": true, "instance": true, "state": true, "device": true},
	"metrics":     {"": true, "instance": true, "records": true, "events": true},
	"form":        {"": true, "config": true},
	"actions":     {"": true, "jobs": true, "manual-jobs": true, "device-actions": true},
	"records":     {"": true, "records": true, "events": true},
	"timeline":    {"": true, "records": true, "events": true},
	"table":       {"": true, "records": true, "bindings": true},
	"schedule":    {"": true, "records": true},
	"chart":       {"": true, "records": true},
	"markdown":    {"": true},
	"diagnostics": {"": true, "diagnostics": true, "device": true, "state": true},
	"custom":      {"": true},
}

var pluginUIPresentations = map[string]bool{
	"": true, "list": true, "timeline": true, "table": true, "cards": true,
}

var pluginUIScopes = map[string]bool{
	"instance.read": true,
	"jobs.run":      true,
	"records.read":  true,
	"bindings.read": true,
	"config.write":  true,
}

// validateManifestUISchema validates each raw UI contribution against the
// embedded plugin UI schema. The root manifest schema intentionally only
// requires ui to be an object; this second pass is the authoritative schema
// validation for the contribution.
func validateManifestUISchema(raw any) error {
	root, ok := asStringMap(raw)
	if !ok {
		return nil
	}
	contributes, ok := asStringMap(root["contributes"])
	if !ok {
		return nil
	}
	validator, err := NewSchemaValidator(cloudpath.PluginUISchema)
	if err != nil {
		return fmt.Errorf("load plugin UI schema: %w", err)
	}
	for _, category := range []string{"drivers", "applications", "connectors"} {
		items, ok := asAnySlice(contributes[category])
		if !ok {
			continue
		}
		for i, item := range items {
			obj, ok := asStringMap(item)
			if !ok {
				continue
			}
			rawUI, exists := obj["ui"]
			if !exists {
				continue
			}
			if category == "connectors" {
				return fmt.Errorf("connectors[%d].ui: Connector UI is not supported", i)
			}
			data, err := json.Marshal(rawUI)
			if err != nil {
				return fmt.Errorf("%s[%d].ui: encode: %w", category, i, err)
			}
			if len(data) > pluginUIMaxBytes {
				return fmt.Errorf("%s[%d].ui: exceeds %d bytes", category, i, pluginUIMaxBytes)
			}
			if err := validator.Validate(rawUI); err != nil {
				return fmt.Errorf("%s[%d].ui: %w", category, i, err)
			}
		}
	}
	return nil
}

func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, v := range m {
			key, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[key] = v
		}
		return out, true
	default:
		return nil, false
	}
}

func asAnySlice(v any) ([]any, bool) {
	switch a := v.(type) {
	case []any:
		return a, true
	case []map[string]any:
		out := make([]any, len(a))
		for i := range a {
			out[i] = a[i]
		}
		return out, true
	default:
		return nil, false
	}
}

// validateManifestUI performs semantic checks that JSON Schema cannot express
// safely: placement, route uniqueness, page identity, section/source pairing
// and custom asset path confinement.
func validateManifestUI(m *Manifest) error {
	if m == nil || m.Contributes == nil {
		return nil
	}
	routes := map[string]string{}
	for i := range m.Contributes.Drivers {
		c := &m.Contributes.Drivers[i]
		if c.UI == nil {
			continue
		}
		if err := validateUIForKind(m.Kind, "Driver", c.UI, routes); err != nil {
			return fmt.Errorf("drivers[%d].ui: %w", i, err)
		}
	}
	for i := range m.Contributes.Applications {
		c := &m.Contributes.Applications[i]
		if c.UI == nil {
			continue
		}
		if err := validateUIForKind(m.Kind, "Application", c.UI, routes); err != nil {
			return fmt.Errorf("applications[%d].ui: %w", i, err)
		}
	}
	return nil
}

func validateUIForKind(pluginKind, contributionKind string, ui *PluginUI, routes map[string]string) error {
	if ui == nil {
		return nil
	}
	if ui.APIVersion != 1 {
		return fmt.Errorf("apiVersion %d is not supported (want 1)", ui.APIVersion)
	}
	data, err := json.Marshal(ui)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if len(data) > pluginUIMaxBytes {
		return fmt.Errorf("contribution exceeds %d bytes", pluginUIMaxBytes)
	}
	if contributionKind == "Driver" {
		if pluginKind != "Driver" {
			return fmt.Errorf("driver UI is only valid on a Driver plugin")
		}
		if ui.Navigation != nil || len(ui.Pages) > 0 {
			return fmt.Errorf("driver UI cannot declare navigation or pages")
		}
		if ui.Device == nil {
			return fmt.Errorf("driver UI must declare device")
		}
		if len(ui.Device.Sections) == 0 || len(ui.Device.Sections) > pluginUIDeviceSection {
			return fmt.Errorf("device sections must contain 1..%d entries", pluginUIDeviceSection)
		}
		for i := range ui.Device.Sections {
			if err := validateUISection(ui.Device.Sections[i], true); err != nil {
				return fmt.Errorf("device.sections[%d]: %w", i, err)
			}
		}
		return nil
	}

	if pluginKind != "Application" {
		return fmt.Errorf("application UI is only valid on an Application plugin")
	}
	if ui.Device != nil {
		return fmt.Errorf("application UI cannot declare device")
	}
	if ui.Navigation == nil || len(ui.Pages) == 0 {
		return fmt.Errorf("application UI must declare navigation and at least one page")
	}
	if err := validateUINavigation(*ui.Navigation, routes); err != nil {
		return err
	}
	if len(ui.Pages) > pluginUIPageMaxCount {
		return fmt.Errorf("pages exceeds %d entries", pluginUIPageMaxCount)
	}
	seenPages := map[string]bool{}
	for i := range ui.Pages {
		page := ui.Pages[i]
		if !pluginUISlugRE.MatchString(page.ID) {
			return fmt.Errorf("pages[%d].id %q is not a valid slug", i, page.ID)
		}
		if seenPages[page.ID] {
			return fmt.Errorf("pages[%d].id %q is duplicated", i, page.ID)
		}
		seenPages[page.ID] = true
		if strings.TrimSpace(page.Title) == "" || len([]rune(page.Title)) > 80 {
			return fmt.Errorf("pages[%d].title must contain 1..80 characters", i)
		}
		if len(page.Sections) == 0 || len(page.Sections) > pluginUISectionMax {
			return fmt.Errorf("pages[%d].sections must contain 1..%d entries", i, pluginUISectionMax)
		}
		for j := range page.Sections {
			if err := validateUISection(page.Sections[j], false); err != nil {
				return fmt.Errorf("pages[%d].sections[%d]: %w", i, j, err)
			}
		}
	}
	return nil
}

func validateUINavigation(n UINavigation, routes map[string]string) error {
	if strings.TrimSpace(n.Title) == "" || len([]rune(n.Title)) > 40 {
		return fmt.Errorf("navigation.title must contain 1..40 characters")
	}
	if !pluginUISlugRE.MatchString(n.Route) {
		return fmt.Errorf("navigation.route %q is not a valid slug", n.Route)
	}
	if n.Order < 0 || n.Order > 999 {
		return fmt.Errorf("navigation.order must be between 0 and 999")
	}
	if n.Visibility != "" && n.Visibility != "instance-enabled" && n.Visibility != "always" {
		return fmt.Errorf("navigation.visibility %q is not supported", n.Visibility)
	}
	if previous, exists := routes[n.Route]; exists {
		return fmt.Errorf("navigation.route %q conflicts with %s", n.Route, previous)
	}
	routes[n.Route] = "another UI contribution"
	return nil
}

func validateUISection(section UISection, driver bool) error {
	allowedSources, ok := pluginUISectionSources[section.Type]
	if !ok {
		return fmt.Errorf("section type %q is not allowlisted", section.Type)
	}
	if !allowedSources[section.Source] {
		return fmt.Errorf("source %q is not valid for section type %q", section.Source, section.Type)
	}
	if driver && section.Type == "custom" {
		return fmt.Errorf("driver UI custom sections are not supported")
	}
	if len([]rune(section.RecordType)) > 64 {
		return fmt.Errorf("recordType exceeds 64 characters")
	}
	if !pluginUIPresentations[section.Presentation] {
		return fmt.Errorf("presentation %q is not supported", section.Presentation)
	}
	if len([]rune(section.Text)) > 4000 {
		return fmt.Errorf("text exceeds 4000 characters")
	}
	if len([]rune(section.Entry)) > pluginUIPathMaxBytes {
		return fmt.Errorf("entry exceeds %d characters", pluginUIPathMaxBytes)
	}
	if section.Type == "markdown" && strings.TrimSpace(section.Text) == "" {
		return fmt.Errorf("markdown section requires text")
	}
	if section.Type != "markdown" && section.Text != "" {
		return fmt.Errorf("text is only valid for markdown sections")
	}
	if section.Type == "custom" {
		if err := validateUIPath(section.Entry); err != nil {
			return err
		}
		if section.Source != "" {
			return fmt.Errorf("custom section must not declare source")
		}
	} else if section.Entry != "" {
		return fmt.Errorf("entry is only valid for custom sections")
	}
	if len(section.Scopes) > 16 {
		return fmt.Errorf("scopes exceeds 16 entries")
	}
	seenScopes := map[string]bool{}
	for _, scope := range section.Scopes {
		if !pluginUIScopes[scope] {
			return fmt.Errorf("scope %q is not allowlisted", scope)
		}
		if seenScopes[scope] {
			return fmt.Errorf("scope %q is duplicated", scope)
		}
		seenScopes[scope] = true
	}
	if section.Type != "custom" && len(section.Scopes) > 0 {
		return fmt.Errorf("scopes are only valid for custom sections")
	}
	if len(section.Fields) > 64 {
		return fmt.Errorf("fields exceeds 64 entries")
	}
	return nil
}

func validateUIPath(raw string) error {
	if raw == "" {
		return fmt.Errorf("custom section requires entry")
	}
	if strings.ContainsAny(raw, "\\\x00") || strings.Contains(raw, "://") || strings.HasPrefix(raw, "/") {
		return fmt.Errorf("custom entry must be a relative plugin path")
	}
	clean := path.Clean(raw)
	if clean != raw || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("custom entry must not escape the plugin package")
	}
	if !strings.HasPrefix(clean, "ui/") || clean == "ui/" {
		return fmt.Errorf("custom entry must be under ui/")
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("custom entry contains an invalid path component")
		}
	}
	return nil
}

func hasCustomUI(ui *PluginUI) bool {
	if ui == nil {
		return false
	}
	for _, page := range ui.Pages {
		for _, section := range page.Sections {
			if section.Type == "custom" && section.Entry != "" {
				return true
			}
		}
	}
	if ui.Device != nil {
		for _, section := range ui.Device.Sections {
			if section.Type == "custom" && section.Entry != "" {
				return true
			}
		}
	}
	return false
}

// HasCustomUI reports whether a manifest declares at least one custom UI entry.
func HasCustomUI(m *Manifest) bool {
	if m == nil || m.Contributes == nil {
		return false
	}
	for i := range m.Contributes.Drivers {
		if hasCustomUI(m.Contributes.Drivers[i].UI) {
			return true
		}
	}
	for i := range m.Contributes.Applications {
		if hasCustomUI(m.Contributes.Applications[i].UI) {
			return true
		}
	}
	return false
}
