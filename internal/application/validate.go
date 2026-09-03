package application

import "fmt"

// Validate checks a proposed binding list against the declared requirements and
// candidate entities. It produces machine-readable issues for: missing required
// capabilities, duplicate occupation (when reuse is not allowed), cross-tenant
// entities, cardinality violations, below-minItems, and version incompatibility.
func (b Binder) Validate(requirements []Requirement, candidates []Candidate, bindings []Binding) ValidationResult {
	var issues []Issue

	reqByID := map[string]Requirement{}
	for _, r := range requirements {
		if err := r.Validate(); err != nil {
			issues = append(issues, Issue{Code: CodeInvalidRequirement, RequirementID: r.ID, Message: err.Error()})
		}
		reqByID[r.ID] = r
	}

	candByID := map[string]Candidate{}
	for _, c := range candidates {
		candByID[c.EntityID] = c
	}

	countByReq := map[string]int{}
	entityReqs := map[string][]string{}

	for _, bnd := range bindings {
		if bnd.RequirementID == "" {
			issues = append(issues, Issue{Code: CodeEmptyRequirementID, EntityID: bnd.EntityID, Message: "binding has empty requirement id"})
			continue
		}
		if bnd.EntityID == "" {
			issues = append(issues, Issue{Code: CodeEmptyEntityID, RequirementID: bnd.RequirementID, Message: "binding has empty entity id"})
			continue
		}
		req, ok := reqByID[bnd.RequirementID]
		if !ok {
			issues = append(issues, Issue{Code: CodeUnknownRequirement, RequirementID: bnd.RequirementID, EntityID: bnd.EntityID, Message: fmt.Sprintf("requirement id %q is not declared", bnd.RequirementID)})
			continue
		}
		cand, ok := candByID[bnd.EntityID]
		if !ok {
			issues = append(issues, Issue{Code: CodeUnknownEntity, RequirementID: bnd.RequirementID, EntityID: bnd.EntityID, Message: fmt.Sprintf("entity id %q is not a known candidate", bnd.EntityID)})
			continue
		}
		if b.TenantID != "" && cand.TenantID != b.TenantID {
			issues = append(issues, Issue{Code: CodeCrossTenant, RequirementID: bnd.RequirementID, EntityID: bnd.EntityID, Message: fmt.Sprintf("entity %q belongs to tenant %q, expected %q", bnd.EntityID, cand.TenantID, b.TenantID)})
			continue
		}
		if ok, code, msg := candidateProvidesCapability(cand, req.Capability); !ok {
			issues = append(issues, Issue{Code: code, RequirementID: bnd.RequirementID, EntityID: bnd.EntityID, Message: msg})
		}
		countByReq[req.ID]++
		entityReqs[bnd.EntityID] = append(entityReqs[bnd.EntityID], bnd.RequirementID)
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

	// Duplicate occupation: an entity bound to more than one requirement (or
	// more than one slot) while none of the occupying requirements allows reuse.
	for entity, reqs := range entityReqs {
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
			issues = append(issues, Issue{Code: CodeDuplicateOccupation, EntityID: entity, Message: fmt.Sprintf("entity %q is occupied by requirements %v while reuse is not allowed", entity, reqs)})
		}
	}

	return ValidationResult{Valid: len(issues) == 0, Issues: issues}
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
