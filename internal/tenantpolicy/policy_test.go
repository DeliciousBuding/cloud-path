package tenantpolicy

import (
	"errors"
	"testing"
)

func TestDefaultPolicyValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
}

func TestPolicyRejectsUnlimitedOrInvalid(t *testing.T) {
	p := Defaults()
	p.Quotas.Devices = 0
	if !errors.Is(p.Validate(), ErrInvalidPolicy) {
		t.Fatal("zero device quota must be rejected")
	}
	p = Defaults()
	p.Retention.Audit = 1
	if !errors.Is(p.Validate(), ErrInvalidPolicy) {
		t.Fatal("audit retention below 7 days must be rejected")
	}
}
