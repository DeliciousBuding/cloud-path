package registry

import (
	"context"
	"fmt"
	"strings"
)

// TrustMode records how an installed plugin's artifact was authenticated.
// It is persisted in plugins.lock and surfaced by inspect/install.
type TrustMode string

const (
	// TrustModeExplicitDigest means the user supplied an independent sha256
	// digest out of band (--digest). This is verified evidence.
	TrustModeExplicitDigest TrustMode = "explicit-digest"
	// TrustModeVerifiedRegistry means a curated Registry entry bound the plugin
	// id/version/source/digest/publisher. This is verified evidence.
	TrustModeVerifiedRegistry TrustMode = "verified-registry"
	// TrustModeAttestation means a build attestation verifier authenticated the
	// artifact. This is verified evidence.
	TrustModeAttestation TrustMode = "attestation"
	// TrustModeUnreviewedTOFU means only the same-origin release checksum was
	// available. It is never treated as verified.
	TrustModeUnreviewedTOFU TrustMode = "unreviewed-tofu"
)

// trustPlan is the trust decision resolved before an install writes anything.
type trustPlan struct {
	mode      TrustMode
	expected  string // lowercase hex sha256; empty means "resolve after download"
	verified  bool
	evidence  string
	publisher string // VerifiedPublisher when mode is VerifiedRegistry
}

// trustModeVerified reports whether a trust mode counts as independent,
// verified evidence (as opposed to unreviewed trust-on-first-use).
func trustModeVerified(mode TrustMode) bool {
	switch mode {
	case TrustModeExplicitDigest, TrustModeVerifiedRegistry, TrustModeAttestation:
		return true
	default:
		return false
	}
}

// AttestationSubject is the artifact an attestation must cover.
type AttestationSubject struct {
	Owner  string
	Name   string
	Digest string // lowercase hex sha256 of the downloaded artifact
}

// Attestation is the non-secret evidence returned by a verifier. Evidence must
// never contain tokens, keys or other secrets; it is persisted to plugins.lock.
type Attestation struct {
	Digest    string
	Predicate string
	Evidence  string
}

// AttestationVerifier authenticates a build attestation for a release asset.
// Implementations must fail closed: an error means the artifact is unverified.
type AttestationVerifier interface {
	Verify(ctx context.Context, subject AttestationSubject) (*Attestation, error)
}

// attestationEvidence renders a short, non-secret evidence label.
func attestationEvidence(att *Attestation) string {
	if att == nil {
		return "build attestation"
	}
	if att.Evidence != "" {
		return att.Evidence
	}
	if att.Predicate != "" {
		return "build attestation (" + att.Predicate + ")"
	}
	return "build attestation"
}

// ValidateRegistryBinding enforces the fixed binding for a verified Registry
// entry: plugin id, version, source, digest and publisher must all match the
// resolved install. Any inconsistency fails closed.
func ValidateRegistryBinding(entry *RegistryEntry, manifest *Manifest, source string) error {
	if entry == nil || manifest == nil {
		return fmt.Errorf("%w: registry entry or manifest is nil", ErrRegistryBindingMismatch)
	}
	if entry.ID != manifest.ID {
		return fmt.Errorf("%w: registry id %q != manifest id %q", ErrRegistryBindingMismatch, entry.ID, manifest.ID)
	}
	if entry.Version != manifest.Version {
		return fmt.Errorf("%w: registry version %q != manifest version %q", ErrRegistryBindingMismatch, entry.Version, manifest.Version)
	}
	if entry.Source != source {
		return fmt.Errorf("%w: registry source %q != resolved source %q", ErrRegistryBindingMismatch, entry.Source, source)
	}
	if _, err := NormalizeDigest(entry.Digest); err != nil {
		return fmt.Errorf("%w: %v", ErrRegistryBindingMismatch, err)
	}
	if strings.TrimSpace(entry.VerifiedPublisher) == "" {
		return fmt.Errorf("%w: registry entry %q has no verifiedPublisher", ErrRegistryBindingMismatch, entry.ID)
	}
	return nil
}

// validateUpdateTrust enforces the update trust invariants before an install
// side effect: an update must not downgrade a verified installation to
// unreviewed TOFU, and it must not change source or publisher without
// confirmation.
func validateUpdateTrust(existing LockedPlugin, repo Repo, plan trustPlan) error {
	if existing.Verified && plan.mode == TrustModeUnreviewedTOFU {
		return fmt.Errorf("%w: plugin %s is verified, refusing downgrade to unreviewed trust-on-first-use", ErrTrustDowngrade, existing.ID)
	}
	if strings.TrimSpace(existing.Source) != "" && existing.Source != repo.URL {
		return fmt.Errorf("%w: source changed from %s to %s without confirmation", ErrTrustDowngrade, existing.Source, repo.URL)
	}
	if existing.VerifiedPublisher != "" && plan.mode == TrustModeVerifiedRegistry &&
		plan.publisher != "" && plan.publisher != existing.VerifiedPublisher {
		return fmt.Errorf("%w: publisher changed from %s to %s without confirmation", ErrTrustDowngrade, existing.VerifiedPublisher, plan.publisher)
	}
	return nil
}
