package registry

import (
	"strings"
	"testing"
)

func TestValidateManifestApplicationUI(t *testing.T) {
	data := []byte(`
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Application
id: io.github.example.app
version: 0.1.0
protocol: 1
entrypoint: ./app
contributes:
  applications:
    - id: scheduled-compartment
      title: Scheduled Compartment
      ui:
        apiVersion: 1
        navigation:
          title: 药盒提醒
          i18n:
            zh-CN: 药盒提醒
            en-US: Pillbox reminders
          icon: pill
          order: 30
          route: pillbox
          visibility: instance-enabled
        pages:
          - id: home
            title: 药盒提醒
            i18n:
              zh-CN: 药盒提醒
              en-US: Pillbox reminders
            sections:
              - type: status
                i18n:
                  zh-CN: 状态
                  en-US: Status
                  en-US.description: Current status
              - type: actions
                source: manual-jobs
              - type: records
                recordType: window
                presentation: timeline
              - type: form
                source: config
`)
	m, err := ValidateManifest(data, testSchema(t))
	if err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	ui := m.Contributes.Applications[0].UI
	if ui == nil || ui.APIVersion != 1 || ui.Navigation == nil || ui.Navigation.Route != "pillbox" {
		t.Fatalf("UI not parsed: %+v", ui)
	}
	if len(ui.Pages) != 1 || len(ui.Pages[0].Sections) != 4 {
		t.Fatalf("UI pages/sections not parsed: %+v", ui)
	}
	if ui.Navigation.I18n["en-US"] != "Pillbox reminders" || ui.Pages[0].I18n["en-US"] != "Pillbox reminders" || ui.Pages[0].Sections[0].I18n["en-US"] != "Status" {
		t.Fatalf("UI i18n not parsed: navigation=%+v page=%+v section=%+v", ui.Navigation.I18n, ui.Pages[0].I18n, ui.Pages[0].Sections[0].I18n)
	}
	public := m.PublicContributions()
	if public.Applications[0].UI.Navigation.I18n["en-US"] != "Pillbox reminders" || public.Applications[0].UI.Pages[0].I18n["en-US"] != "Pillbox reminders" || public.Applications[0].UI.Pages[0].Sections[0].I18n["en-US"] != "Status" {
		t.Fatalf("UI i18n not projected: %+v", public.Applications[0].UI)
	}
}

func TestValidateManifestDriverUI(t *testing.T) {
	data := []byte(`
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: io.github.example.driver
version: 0.1.0
protocol: 1
entrypoint: ./driver
contributes:
  drivers:
    - id: stcb
      title: STC-B
      ui:
        apiVersion: 1
        device:
          sections:
            - type: status
            - type: actions
              source: device-actions
            - type: diagnostics
              source: diagnostics
`)
	m, err := ValidateManifest(data, testSchema(t))
	if err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	if m.Contributes.Drivers[0].UI == nil || m.Contributes.Drivers[0].UI.Device == nil {
		t.Fatalf("driver UI not parsed: %+v", m.Contributes.Drivers[0].UI)
	}
}

func TestValidateManifestUIRejectsInvalidContracts(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unknown section",
			yaml: applicationWithUI(`        navigation:
          title: Test
          route: test
        pages:
          - id: home
            title: Test
            sections:
              - type: raw-html
`),
			want: "not in enum",
		},
		{
			name: "invalid route",
			yaml: applicationWithUI(`        navigation:
          title: Test
          route: Bad_Route
        pages:
          - id: home
            title: Test
            sections:
              - type: status
`),
			want: "does not match pattern",
		},
		{
			name: "path escape",
			yaml: applicationWithUI(`        navigation:
          title: Test
          route: test
        pages:
          - id: home
            title: Test
            sections:
              - type: custom
                entry: ui/../secret.html
`),
			want: "must not escape",
		},
		{
			name: "unknown ui field",
			yaml: applicationWithUI(`        navigation:
          title: Test
          route: test
        pages:
          - id: home
            title: Test
            sections:
              - type: status
        extra: true
`),
			want: "additional property",
		},
		{
			name: "invalid scope",
			yaml: applicationWithUI(`        navigation:
          title: Test
          route: test
        pages:
          - id: home
            title: Test
            sections:
              - type: custom
                entry: ui/index.html
                scopes: [admin]
`),
			want: "not in enum",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateManifest([]byte(tc.yaml), testSchema(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateManifestUIRejectsWrongPlacementAndRouteConflict(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "driver navigation",
			yaml: `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: io.github.example.driver
version: 0.1.0
protocol: 1
entrypoint: ./driver
contributes:
  drivers:
    - id: stcb
      ui:
        apiVersion: 1
        navigation:
          title: Test
          route: test
`,
			want: "cannot declare navigation",
		},
		{
			name: "application device",
			yaml: applicationWithUI(`        device:
          sections:
            - type: status
`),
			want: "cannot declare device",
		},
		{
			name: "duplicate route",
			yaml: `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Application
id: io.github.example.app
version: 0.1.0
protocol: 1
entrypoint: ./app
contributes:
  applications:
    - id: one
      ui:
        apiVersion: 1
        navigation: {title: One, route: same}
        pages: [{id: home, title: One, sections: [{type: status}]}]
    - id: two
      ui:
        apiVersion: 1
        navigation: {title: Two, route: same}
        pages: [{id: home, title: Two, sections: [{type: status}]}]
`,
			want: "conflicts",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateManifest([]byte(tc.yaml), testSchema(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateManifestConnectorUIRejectedAndLegacyCompatible(t *testing.T) {
	connector := []byte(`apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Connector
id: io.github.example.connector
version: 0.1.0
protocol: 1
entrypoint: ./connector
contributes:
  connectors:
    - id: mqtt
      ui:
        apiVersion: 1
        navigation: {title: Test, route: test}
`)
	if _, err := ValidateManifest(connector, testSchema(t)); err == nil || !strings.Contains(err.Error(), "Connector UI") {
		t.Fatalf("connector UI must fail closed, got %v", err)
	}

	legacy := []byte(`apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Application
id: io.github.example.legacy
version: 0.1.0
protocol: 1
entrypoint: ./app
contributes:
  applications:
    - id: legacy
      title: Legacy
`)
	m, err := ValidateManifest(legacy, testSchema(t))
	if err != nil || m.Contributes.Applications[0].UI != nil {
		t.Fatalf("legacy manifest without ui must remain valid: m=%+v err=%v", m, err)
	}
}

func applicationWithUI(ui string) string {
	return `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Application
id: io.github.example.app
version: 0.1.0
protocol: 1
entrypoint: ./app
contributes:
  applications:
    - id: app
      ui:
        apiVersion: 1
` + ui
}
