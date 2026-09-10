package application

import (
	"errors"
	"fmt"
	"strings"
)

// Validate checks a proposed binding list against the declared requirements and
// candidate entities. It produces machine-readable issues for: missing required
// capabilities, duplicate occupation (when reuse is not allowed), cross-tenant
// entities, cardinality violations, below-minItems, and version incompatibility.
func validateRequirementList(rs []Requirement) error {
	var errs []error
	seen := map[string]bool{}
	for i := range rs {
		r := &rs[i]
		if r.ID != "" && seen[r.ID] {
			errs = append(errs, fmt.Errorf("requirement %q: duplicate id", r.ID))
		}
		seen[r.ID] = true
		if err := r.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b Binder) Validate(requirements []Requirement, candidates []Candidate, bindings []Binding) ValidationResult {
	var issues []Issue

	reqByID := map[string]Requirement{}
	for _, r := range requirements {
		if err := r.Validate(); err != nil {
			issues = append(issues, Issue{Code: CodeInvalidRequirement, RequirementID: r.ID, Message: err.Error()})
		}
		reqByID[r.ID] = r
	}

	countByReq := map[string]int{}
	targetReqs := map[string][]string{}
	targetLabels := map[string]string{}
	entityDevices := map[string]map[string]bool{}

	for _, bnd := range bindings {
		if bnd.RequirementID == "" {
			issues = append(issues, Issue{Code: CodeEmptyRequirementID, EntityID: bnd.EntityID, DeviceID: bnd.DeviceID, Message: "binding has empty requirement id"})
			continue
		}
		if bnd.EntityID == "" {
			issues = append(issues, Issue{Code: CodeEmptyEntityID, RequirementID: bnd.RequirementID, DeviceID: bnd.DeviceID, Message: "binding has empty entity id"})
			continue
		}
		req, ok := reqByID[bnd.RequirementID]
		if !ok {
			issues = append(issues, Issue{Code: CodeUnknownRequirement, RequirementID: bnd.RequirementID, EntityID: bnd.EntityID, DeviceID: bnd.DeviceID, Message: fmt.Sprintf("requirement id %q is not declared", bnd.RequirementID)})
			continue
		}
		cand, matches := candidateForBinding(candidates, bnd)
		if matches == 0 {
			issues = append(issues, Issue{Code: CodeUnknownEntity, RequirementID: bnd.RequirementID, EntityID: bnd.EntityID, DeviceID: bnd.DeviceID, Message: fmt.Sprintf("entity id %q is not a known candidate on device %q", bnd.EntityID, bnd.DeviceID)})
			continue
		}
		if matches > 1 {
			issues = append(issues, Issue{Code: CodeAmbiguousEntity, RequirementID: bnd.RequirementID, EntityID: bnd.EntityID, DeviceID: bnd.DeviceID, Message: fmt.Sprintf("entity id %q matches multiple devices; device_id is required", bnd.EntityID)})
			continue
		}
		if b.TenantID != "" && cand.TenantID != b.TenantID {
			issues = append(issues, Issue{Code: CodeCrossTenant, RequirementID: bnd.RequirementID, EntityID: bnd.EntityID, DeviceID: cand.DeviceID, Message: fmt.Sprintf("entity %q on device %q belongs to tenant %q, expected %q", bnd.EntityID, cand.DeviceID, cand.TenantID, b.TenantID)})
			continue
		}
		if ok, code, msg := candidateProvidesCapability(cand, req.Capability); !ok {
			issues = append(issues, Issue{Code: code, RequirementID: bnd.RequirementID, EntityID: bnd.EntityID, DeviceID: cand.DeviceID, Message: msg})
		}
		key := candidateTargetKey(cand.DeviceID, cand.EntityID)
		countByReq[req.ID]++
		targetReqs[key] = append(targetReqs[key], bnd.RequirementID)
		targetLabels[key] = targetLabel(cand.DeviceID, cand.EntityID)
		if cand.DeviceID != "" {
			if entityDevices[cand.EntityID] == nil {
				entityDevices[cand.EntityID] = map[string]bool{}
			}
			entityDevices[cand.EntityID][cand.DeviceID] = true
		}
	}

	// Cardinality checks per declared requirement.
	for _, req := range requirements {
		n := countByReq[req.ID]
		switch req.Cardinality {
		case CardinalityOne:
			if n == 0 {
				issues = append(issues, missingRequiredIssue(req))
			} else if n > 1 {
				issues = append(issues, Issue{Code: CodeCardinalityViolation, RequirementID: req.ID, Message: fmt.Sprintf("requirement %q expects exactly one entity, got %d", req.ID, n)})
			}
		case CardinalityZeroOrOne:
			if n > 1 {
				issues = append(issues, Issue{Code: CodeCardinalityViolation, RequirementID: req.ID, Message: fmt.Sprintf("requirement %q allows at most one entity, got %d", req.ID, n)})
			}
		case CardinalityOneOrMore:
			minN := req.MinItems
			if minN < 1 {
				minN = 1
			}
			if n == 0 {
				issues = append(issues, missingRequiredIssue(req))
			} else if n < minN {
				issues = append(issues, Issue{Code: CodeBelowMinItems, RequirementID: req.ID, Message: fmt.Sprintf("requirement %q needs at least %d entities, got %d", req.ID, minN, n)})
			}
		}
	}

	// Duplicate occupation is per provider target, not per local entity id:
	// two different boards may each legitimately expose "buzzer" or "key1".
	for key, reqs := range targetReqs {
		if len(reqs) <= 1 {
			continue
		}
		allowed := false
		for _, rid := range reqs {
			if req, ok := reqByID[rid]; ok && req.AllowReuse {
				allowed = true
				break
			}
		}
		if !allowed {
			issues = append(issues, Issue{Code: CodeDuplicateOccupation, EntityID: targetEntityID(key), Message: fmt.Sprintf("target %s is occupied by requirements %v while reuse is not allowed", targetLabels[key], reqs)})
		}
	}

	for entityID, devices := range entityDevices {
		if len(devices) <= 1 {
			continue
		}
		issues = append(issues, Issue{
			Code: CodeAmbiguousEntity, EntityID: entityID,
			Message: fmt.Sprintf("entity id %q is bound to multiple devices; application protocol requires distinct entity ids per target", entityID),
		})
	}

	return ValidationResult{Valid: len(issues) == 0, Issues: issues}
}

func candidateForBinding(candidates []Candidate, bnd Binding) (Candidate, int) {
	var first Candidate
	matches := 0
	for _, candidate := range candidates {
		if candidate.EntityID != bnd.EntityID {
			continue
		}
		if bnd.DeviceID != "" && candidate.DeviceID != bnd.DeviceID {
			continue
		}
		if matches == 0 {
			first = candidate
		}
		matches++
	}
	return first, matches
}

func targetLabel(deviceID, entityID string) string {
	if deviceID == "" {
		return fmt.Sprintf("entity %q", entityID)
	}
	return fmt.Sprintf("entity %q on device %q", entityID, deviceID)
}

func targetEntityID(key string) string {
	if i := strings.LastIndexByte(key, 0); i >= 0 {
		return key[i+1:]
	}
	return key
}

func missingRequiredIssue(req Requirement) Issue {
	return Issue{
		Code:          CodeMissingRequired,
		RequirementID: req.ID,
		Message:       fmt.Sprintf("requirement %q has no bound entity", req.ID),
	}
}

func missingRequiredResult(req Requirement) ValidationResult {
	return ValidationResult{Valid: false, Issues: []Issue{missingRequiredIssue(req)}}
}

func duplicateOccupationResult(req Requirement, entity string) ValidationResult {
	return ValidationResult{Valid: false, Issues: []Issue{{
		Code:          CodeDuplicateOccupation,
		RequirementID: req.ID,
		EntityID:      entity,
		Message:       fmt.Sprintf("no free entity for requirement %q; all matching entities are occupied and reuse is not allowed", req.ID),
	}}}
}

func belowMinItemsResult(req Requirement, minN int) ValidationResult {
	return ValidationResult{Valid: false, Issues: []Issue{{
		Code:          CodeBelowMinItems,
		RequirementID: req.ID,
		Message:       fmt.Sprintf("requirement %q needs at least %d entities, but not enough are available", req.ID, minN),
	}}}
}
