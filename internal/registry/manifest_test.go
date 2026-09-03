package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSchema(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "spec", "plugin-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestManifestValidation(t *testing.T) {
	data := []byte(`
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: io.github.example.driver
version: 0.1.0
protocol: 1
entrypoint: ./driver
compatibility:
  core: ">=0.1.0 <0.2.0"
permissions:
  hardware: [serial]
  network: [outbound]
`)
	m, err := ValidateManifest(data, testSchema(t))
	if err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	if m.ID != "io.github.example.driver" || m.Kind != "Driver" || m.Protocol != 1 {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if m.Compatibility.Core != ">=0.1.0 <0.2.0" {
		t.Fatalf("compatibility = %q", m.Compatibility.Core)
	}
}

func TestManifestRejectBadKind(t *testing.T) {
	data := []byte(`
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Wizard
id: io.github.example.driver
version: 0.1.0
protocol: 1
entrypoint: ./driver
`)
	_, err := ValidateManifest(data, testSchema(t))
	if err == nil {
		t.Fatal("bad kind should be rejected")
	}
	if !strings.Contains(err.Error(), "enum") {
		t.Fatalf("expected enum error, got: %v", err)
	}
}

func TestManifestDriverContributions(t *testing.T) {
	data := []byte(`
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: io.github.example.driver
version: 0.1.0
protocol: 1
entrypoint: ./driver
compatibility:
  core: ">=0.1.0 <0.2.0"
contributes:
  drivers:
    - id: stcb
      title: STC-B
      discovery: manual
    - id: stcb-tcp
      title: STC-B over TCP
`)
	m, err := ValidateManifest(data, testSchema(t))
	if err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	if m.Contributes == nil {
		t.Fatal("expected contributes block")
	}
	if len(m.Contributes.Drivers) != 2 {
		t.Fatalf("drivers = %d, want 2", len(m.Contributes.Drivers))
	}
	if m.Contributes.Drivers[0].ID != "stcb" {
		t.Fatalf("drivers[0].ID = %q, want stcb", m.Contributes.Drivers[0].ID)
	}
	if m.Contributes.Drivers[1].ID != "stcb-tcp" {
		t.Fatalf("drivers[1].ID = %q, want stcb-tcp", m.Contributes.Drivers[1].ID)
	}

	appData := []byte(`
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
`)
	am, err := ValidateManifest(appData, testSchema(t))
	if err != nil {
		t.Fatalf("ValidateManifest(app): %v", err)
	}
	if len(am.Contributes.Applications) != 1 || am.Contributes.Applications[0].ID != "scheduled-compartment" {
		t.Fatalf("unexpected applications contribution: %+v", am.Contributes.Applications)
	}
}

func TestRejectKindContributionMismatch(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "driver only contributes app",
			yaml: `
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: io.github.example.driver
version: 0.1.0
protocol: 1
entrypoint: ./driver
contributes:
  applications:
    - id: alarm
`,
		},
		{
			name: "application only contributes driver",
			yaml: `
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Application
id: io.github.example.app
version: 0.1.0
protocol: 1
entrypoint: ./app
contributes:
  drivers:
    - id: stcb
`,
		},
		{
			name: "connector contributes driver",
			yaml: `
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Connector
id: io.github.example.conn
version: 0.1.0
protocol: 1
entrypoint: ./conn
contributes:
  drivers:
    - id: stcb
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateManifest([]byte(tc.yaml), testSchema(t))
			if err == nil {
				t.Fatal("kind/contributes mismatch must be rejected")
			}
			if !strings.Contains(err.Error(), "must contribute") {
				t.Fatalf("expected mismatch error, got: %v", err)
			}
		})
	}
}

func TestRejectDuplicateContributionID(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "duplicate driver id",
			yaml: `
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: io.github.example.driver
version: 0.1.0
protocol: 1
entrypoint: ./driver
contributes:
  drivers:
    - id: stcb
    - id: stcb
`,
			want: "duplicate contribution id",
		},
		{
			name: "empty application id",
			yaml: `
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Application
id: io.github.example.app
version: 0.1.0
protocol: 1
entrypoint: ./app
contributes:
  applications:
    - id: ""
`,
			want: "contribution id is empty",
		},
		{
			name: "path separator in connector id",
			yaml: `
apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Connector
id: io.github.example.conn
version: 0.1.0
protocol: 1
entrypoint: ./conn
contributes:
  connectors:
    - id: bad/id
`,
			want: "path separator",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateManifest([]byte(tc.yaml), testSchema(t))
			if err == nil {
				t.Fatal("invalid contribution id must be rejected")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in error, got: %v", tc.want, err)
			}
		})
	}
}

func TestValidateContributionsRejectsUnsafeID(t *testing.T) {
	m := &Manifest{
		Kind: "Driver",
		Contributes: &Contributes{
			Drivers: []DriverContribution{{ID: "a\x01b"}},
		},
	}
	if err := ValidateContributions(m); err == nil {
		t.Fatal("control character in contribution id must be rejected")
	}

	ok := &Manifest{
		Kind: "Driver",
		Contributes: &Contributes{
			Drivers: []DriverContribution{{ID: "stcb"}},
		},
	}
	if err := ValidateContributions(ok); err != nil {
		t.Fatalf("valid contribution must pass: %v", err)
	}
}
