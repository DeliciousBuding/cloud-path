package registry

import "testing"

func TestRegistryEntry(t *testing.T) {
	entry := RegistryEntry{
		ID:                "io.github.example.driver",
		Version:           "0.1.0",
		Kind:              "Driver",
		Source:            "https://github.com/example/driver",
		Digest:            "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		VerifiedPublisher: "example",
		Protocol:          1,
		Compatibility:     ">=0.1.0 <0.2.0",
	}
	if err := ValidateRegistryEntry(entry); err != nil {
		t.Fatalf("ValidateRegistryEntry: %v", err)
	}
	entry.Kind = "Wizard"
	if err := ValidateRegistryEntry(entry); err == nil {
		t.Fatal("bad kind should be rejected")
	}
}
