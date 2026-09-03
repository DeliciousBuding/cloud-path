// Package application implements the device-independent Application Binding
// engine for CloudPath.
//
// An Application Plugin declares Capability Requirements and binds them to
// stable Entity IDs. Matching and validation consider only Capability ID /
// version compatibility and tenant scope. The engine must never reference a
// Driver ID, a serial port, or any concrete hardware field. A binding persists
// the stable entity_id; renames, Edge reconnects and Driver restarts must not
// change an existing binding.
//
// The design contract lives in docs/architecture/capability-model.md
// (Application Binding, Reference Application, Compatibility & Migration and
// Invariants).
package application

import (
	"errors"
	"fmt"
	"strings"
)

// Cardinality declares how many entities a Requirement accepts.
type Cardinality string

const (
	// CardinalityOne requires exactly one bound entity.
	CardinalityOne Cardinality = "one"
	// CardinalityZeroOrOne accepts zero or one bound entity.
	CardinalityZeroOrOne Cardinality = "zero-or-one"
	// CardinalityOneOrMore requires at least one bound entity, and at least
	// minItems distinct entities when minItems is set.
	CardinalityOneOrMore Cardinality = "one-or-more"
)

// Valid reports whether c is a supported cardinality.
func (c Cardinality) Valid() bool {
	switch c {
	case CardinalityOne, CardinalityZeroOrOne, CardinalityOneOrMore:
		return true
	}
	return false
}

// Requirement is one Capability the Application needs. Matching resolves each
// Requirement to a Candidate Entity.
type Requirement struct {
	// ID is the requirement's stable identifier within the manifest.
	ID string `json:"id" yaml:"id"`
	// Capability is the full capability reference, e.g.
	// cloudpath.dev/capability/alarm@1.
	Capability string `json:"capability" yaml:"capability"`
	// Cardinality is the number of entities this requirement accepts.
	Cardinality Cardinality `json:"cardinality" yaml:"cardinality"`
	// MinItems is the minimum number of distinct entities for one-or-more.
	MinItems int `json:"minItems,omitempty" yaml:"minItems,omitempty"`
	// AllowReuse permits the same entity to satisfy multiple requirements (or
	// multiple slots of this requirement). Default false means an entity may be
	// occupied by at most one requirement slot.
	AllowReuse bool `json:"allow_reuse,omitempty" yaml:"allow_reuse,omitempty"`
}

// Validate checks a Requirement against the binding contract.
func (r Requirement) Validate() error {
	var errs []error
	if r.ID == "" {
		errs = append(errs, errors.New("requirement id must not be empty"))
	}
	if r.Capability == "" {
		errs = append(errs, fmt.Errorf("requirement %q capability must not be empty", r.ID))
	} else if _, err := parseCapabilityRef(r.Capability); err != nil {
		errs = append(errs, err)
	}
	if !r.Cardinality.Valid() {
		errs = append(errs, fmt.Errorf("requirement %q invalid cardinality %q", r.ID, r.Cardinality))
	}
	if r.MinItems < 0 {
		errs = append(errs, fmt.Errorf("requirement %q min_items must be non-negative", r.ID))
	}
	switch r.Cardinality {
	case CardinalityOne, CardinalityZeroOrOne:
		if r.MinItems > 1 {
			errs = append(errs, fmt.Errorf("requirement %q cardinality %s cannot set min_items > 1", r.ID, r.Cardinality))
		}
	}
	return errors.Join(errs...)
}

// Candidate is the bindable view of an Entity. Only EntityID, TenantID and
// Capabilities participate in matching and validation. Name, DeviceID and
// DriverID are observational metadata; the engine MUST never use them to
// select or accept a binding. This is the device-independent boundary: an
// entity may be produced by any Driver, at any port, on any device.
type Candidate struct {
	EntityID     string   `json:"entity_id" yaml:"entity_id"`
	Name         string   `json:"name,omitempty" yaml:"name,omitempty"`
	TenantID     string   `json:"tenant_id" yaml:"tenant_id"`
	Capabilities []string `json:"capabilities" yaml:"capabilities"`
	// DeviceID is metadata only. Not used for matching.
	DeviceID string `json:"device_id,omitempty" yaml:"device_id,omitempty"`
	// DriverID is metadata only. Not used for matching.
	DriverID string `json:"driver_id,omitempty" yaml:"driver_id,omitempty"`
}

// Binding pairs a Requirement with the stable entity_id that satisfies it.
// Bindings persist the entity_id only; the display name, port and connection
// are intentionally not stored.
type Binding struct {
	RequirementID string `json:"requirement_id" yaml:"requirement_id"`
	EntityID      string `json:"entity_id" yaml:"entity_id"`
}

// BindingSet is the set of Bindings for one Application instance in a tenant.
type BindingSet struct {
	ApplicationID    string    `json:"application_id"`
	PluginInstanceID string    `json:"plugin_instance_id"`
	TenantID         string    `json:"tenant_id"`
	Bindings         []Binding `json:"bindings"`
}

// Issue is one validation finding with a machine-readable code. The code is
// stable and intended for Core/UI to branch on; the message is human-facing.
type Issue struct {
	Code          string `json:"code"`
	RequirementID string `json:"requirement_id,omitempty"`
	EntityID      string `json:"entity_id,omitempty"`
	Message       string `json:"message"`
}

// ValidationResult aggregates binding validation findings.
type ValidationResult struct {
	Valid  bool    `json:"valid"`
	Issues []Issue `json:"issues"`
}

// HasCode reports whether any issue carries the given machine-readable code.
func (r ValidationResult) HasCode(code string) bool {
	for _, i := range r.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

// Machine-readable issue codes.
const (
	// CodeMissingRequired means a required (one or one-or-more) requirement has
	// no bound entity.
	CodeMissingRequired = "missing_required_capability"
	// CodeCapabilityMismatch means the bound entity does not provide the
	// required capability family.
	CodeCapabilityMismatch = "capability_mismatch"
	// CodeVersionIncompatible means the entity provides the capability family
	// but an older/incompatible version.
	CodeVersionIncompatible = "version_incompatible"
	// CodeCardinalityViolation means more entities than the cardinality allows.
	CodeCardinalityViolation = "cardinality_violation"
	// CodeBelowMinItems means fewer than minItems distinct entities.
	CodeBelowMinItems = "below_min_items"
	// CodeDuplicateOccupation means an entity is occupied by multiple
	// requirements (or slots) while none of them allows reuse.
	CodeDuplicateOccupation = "duplicate_occupation"
	// CodeCrossTenant means a binding references an entity outside the
	// application instance's tenant.
	CodeCrossTenant = "cross_tenant"
	// CodeUnknownRequirement means a binding references a requirement id that
	// the manifest does not declare.
	CodeUnknownRequirement = "unknown_requirement"
	// CodeUnknownEntity means a binding references an entity id that is not a
	// known candidate.
	CodeUnknownEntity = "unknown_entity"
	// CodeEmptyRequirementID means a binding has no requirement id.
	CodeEmptyRequirementID = "empty_requirement_id"
	// CodeEmptyEntityID means a binding has no entity id.
	CodeEmptyEntityID = "empty_entity_id"
	// CodeInvalidCapability means a capability reference cannot be parsed.
	CodeInvalidCapability = "invalid_capability"
	// CodeInvalidRequirement means a declared requirement violates the contract.
	CodeInvalidRequirement = "invalid_requirement"
)

// BindingError wraps a failed binding validation so callers can branch on
// machine-readable codes. It implements error.
type BindingError struct {
	Result ValidationResult
}

// Error returns a compact summary of the failing codes.
func (e *BindingError) Error() string {
	if e == nil {
		return ""
	}
	codes := make([]string, 0, len(e.Result.Issues))
	for _, i := range e.Result.Issues {
		codes = append(codes, i.Code)
	}
	return "application binding invalid: " + strings.Join(codes, ",")
}
