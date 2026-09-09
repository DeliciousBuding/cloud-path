package registry

import "github.com/DeliciousBuding/cloud-path/internal/api"

// PublicContributions maps a manifest's typed contributions to the public API
// DTO. This is the single whitelist projection used by Edge and AppHost
// installation status; local paths, entrypoints and requirement payloads are
// never copied.
func (m *Manifest) PublicContributions() api.PluginContributionsData {
	if m == nil || m.Contributes == nil {
		return api.PluginContributionsData{}
	}
	out := api.PluginContributionsData{
		Drivers:      make([]api.PluginDriverContributionData, 0, len(m.Contributes.Drivers)),
		Applications: make([]api.PluginApplicationContributionData, 0, len(m.Contributes.Applications)),
		Connectors:   make([]api.PluginConnectorContributionData, 0, len(m.Contributes.Connectors)),
	}
	for i := range m.Contributes.Drivers {
		d := m.Contributes.Drivers[i]
		out.Drivers = append(out.Drivers, api.PluginDriverContributionData{
			ID: d.ID, Title: d.Title, I18n: d.I18n, Discovery: d.Discovery, UI: publicPluginUI(d.UI),
		})
	}
	for i := range m.Contributes.Applications {
		a := m.Contributes.Applications[i]
		out.Applications = append(out.Applications, api.PluginApplicationContributionData{
			ID: a.ID, Title: a.Title, I18n: a.I18n, UI: publicPluginUI(a.UI),
		})
	}
	for i := range m.Contributes.Connectors {
		c := m.Contributes.Connectors[i]
		out.Connectors = append(out.Connectors, api.PluginConnectorContributionData{
			ID: c.ID, Title: c.Title, I18n: c.I18n, Direction: c.Direction, Host: c.Host,
		})
	}
	return out
}

func publicPluginUI(in *PluginUI) *api.PluginUIData {
	if in == nil {
		return nil
	}
	out := &api.PluginUIData{APIVersion: in.APIVersion}
	if in.Navigation != nil {
		out.Navigation = &api.PluginUINavigationData{
			Title: in.Navigation.Title, I18n: in.Navigation.I18n, Icon: in.Navigation.Icon,
			Order: in.Navigation.Order, Route: in.Navigation.Route,
			Visibility: in.Navigation.Visibility,
		}
	}
	if len(in.Pages) > 0 {
		out.Pages = make([]api.PluginUIPageData, 0, len(in.Pages))
		for _, page := range in.Pages {
			out.Pages = append(out.Pages, api.PluginUIPageData{
				ID: page.ID, Title: page.Title, Description: page.Description,
				I18n: page.I18n, Sections: publicUISections(page.Sections),
			})
		}
	}
	if in.Device != nil {
		out.Device = &api.PluginUIDeviceData{Sections: publicUISections(in.Device.Sections)}
	}
	return out
}

func publicUISections(in []UISection) []api.PluginUISectionData {
	if len(in) == 0 {
		return nil
	}
	out := make([]api.PluginUISectionData, 0, len(in))
	for _, section := range in {
		out = append(out, api.PluginUISectionData{
			Type: section.Type, Title: section.Title, Description: section.Description,
			EmptyText: section.EmptyText, I18n: section.I18n, Source: section.Source,
			RecordType: section.RecordType, Presentation: section.Presentation,
			Text: section.Text, Entry: section.Entry,
			Scopes: append([]string(nil), section.Scopes...), Fields: cloneUIFields(section.Fields),
		})
	}
	return out
}

func cloneUIFields(in []map[string]any) []map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(in))
	for _, field := range in {
		if field == nil {
			out = append(out, nil)
			continue
		}
		clone := make(map[string]any, len(field))
		for k, v := range field {
			clone[k] = v
		}
		out = append(out, clone)
	}
	return out
}
