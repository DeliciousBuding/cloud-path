package api

// PluginUIData is the public, declarative UI contribution attached to a Driver
// or Application contribution. It contains no local paths, entrypoints or
// secrets; custom entry values are package-relative paths.
type PluginUIData struct {
	APIVersion int                     `json:"apiVersion"`
	Navigation *PluginUINavigationData `json:"navigation,omitempty"`
	Pages      []PluginUIPageData      `json:"pages,omitempty"`
	Device     *PluginUIDeviceData     `json:"device,omitempty"`
}

// PluginUINavigationData is the stable Application navigation contribution.
type PluginUINavigationData struct {
	Title      string            `json:"title"`
	I18n       map[string]string `json:"i18n,omitempty"`
	Icon       string            `json:"icon,omitempty"`
	Order      int               `json:"order,omitempty"`
	Route      string            `json:"route"`
	Visibility string            `json:"visibility,omitempty"`
}

// PluginUIPageData is one Application page declaration.
type PluginUIPageData struct {
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	Description string                `json:"description,omitempty"`
	I18n        map[string]string     `json:"i18n,omitempty"`
	Sections    []PluginUISectionData `json:"sections"`
}

// PluginUIDeviceData extends a Driver's device detail page.
type PluginUIDeviceData struct {
	Sections []PluginUISectionData `json:"sections,omitempty"`
}

// PluginUISectionData is one allowlisted Core-rendered section.
type PluginUISectionData struct {
	Type         string            `json:"type"`
	Title        string            `json:"title,omitempty"`
	Description  string            `json:"description,omitempty"`
	EmptyText    string            `json:"emptyText,omitempty"`
	Source       string            `json:"source,omitempty"`
	RecordType   string            `json:"recordType,omitempty"`
	Presentation string            `json:"presentation,omitempty"`
	Text         string            `json:"text,omitempty"`
	Entry        string            `json:"entry,omitempty"`
	Scopes       []string          `json:"scopes,omitempty"`
	I18n         map[string]string `json:"i18n,omitempty"`
	Fields       []map[string]any  `json:"fields,omitempty"`
}
