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
