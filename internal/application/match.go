package application

// Binder carries the tenant scope and instance identity for a binding
// operation. The engine matches and validates candidates purely by Capability
// and tenant; a Binder never couples to a Driver.
type Binder struct {
	ApplicationID    string
	PluginInstanceID string
	TenantID         string
}

// Match auto-selects a candidate entity for every requirement within the
// binder's tenant scope. It returns a fully valid BindingSet, or a
// *BindingError carrying the machine-readable issues when no valid set can be
// produced. Matching only uses Capability ID/version compatibility and tenant;
// it never references Driver ID, a port, or a hardware field.
func (b Binder) Match(requirements []Requirement, candidates []Candidate) (BindingSet, error) {
	if err := validateRequirementList(requirements); err != nil {
		return BindingSet{}, &BindingError{Result: ValidationResult{
			Valid:  false,
			Issues: []Issue{{Code: CodeInvalidRequirement, Message: err.Error()}},
		}}
	}

	inScope := tenantCandidates(candidates, b.TenantID)
	used := map[string]bool{}
	var bindings []Binding

	for _, req := range requirements {
		picked, err := matchRequirement(req, inScope, used)
		if err != nil {
			if berr, ok := err.(*BindingError); ok {
				return BindingSet{}, berr
			}
			return BindingSet{}, err
		}
		for _, entity := range picked {
			bindings = append(bindings, Binding{RequirementID: req.ID, EntityID: entity})
			used[entity] = true
		}
	}

	bs := BindingSet{
		ApplicationID:    b.ApplicationID,
		PluginInstanceID: b.PluginInstanceID,
		TenantID:         b.TenantID,
		Bindings:         bindings,
	}

	res := b.Validate(requirements, candidates, bindings)
	if !res.Valid {
		return BindingSet{}, &BindingError{Result: res}
	}
	return bs, nil
}

// matchRequirement resolves one requirement to a list of entity IDs. It picks
// unused candidates first, and only falls back to already-used entities when
// the requirement allows reuse.
func matchRequirement(req Requirement, candidates []Candidate, used map[string]bool) ([]string, error) {
	var avail []Candidate
	for _, c := range candidates {
		if ok, _, _ := candidateProvidesCapability(c, req.Capability); ok {
			avail = append(avail, c)
		}
	}

	switch req.Cardinality {
	case CardinalityZeroOrOne:
		if len(avail) == 0 {
			return nil, nil
		}
		e := firstUnused(avail, used, req.AllowReuse)
		if e == "" {
			return nil, &BindingError{Result: duplicateOccupationResult(req, "")}
		}
		return []string{e}, nil
	case CardinalityOne:
		if len(avail) == 0 {
			return nil, &BindingError{Result: missingRequiredResult(req)}
		}
		e := firstUnused(avail, used, req.AllowReuse)
		if e == "" {
			return nil, &BindingError{Result: duplicateOccupationResult(req, "")}
		}
		return []string{e}, nil
	case CardinalityOneOrMore:
		n := req.MinItems
		if n < 1 {
			n = 1
		}
		if len(avail) == 0 {
			return nil, &BindingError{Result: missingRequiredResult(req)}
		}
		picked := pickN(avail, used, req.AllowReuse, n)
		if len(picked) < n {
			return nil, &BindingError{Result: belowMinItemsResult(req, n)}
		}
		return picked, nil
	default:
		return nil, &BindingError{Result: ValidationResult{
			Valid:  false,
			Issues: []Issue{{Code: CodeInvalidRequirement, RequirementID: req.ID, Message: "unknown cardinality"}},
		}}
	}
}

// tenantCandidates filters candidates to the binder's tenant. An empty tenant
// means "no tenant filtering" (tests / scaffolding).
func tenantCandidates(candidates []Candidate, tenant string) []Candidate {
	if tenant == "" {
		return candidates
	}
	out := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if c.TenantID == tenant {
			out = append(out, c)
		}
	}
	return out
}

// firstUnused returns the first candidate that is not occupied, or, when
// allowReuse is set, the first candidate regardless of occupation.
func firstUnused(avail []Candidate, used map[string]bool, allowReuse bool) string {
	for _, c := range avail {
		if !used[c.EntityID] {
			return c.EntityID
		}
	}
	if allowReuse && len(avail) > 0 {
		return avail[0].EntityID
	}
	return ""
}

// pickN selects up to n distinct candidate entity IDs, preferring unused ones.
// If allowReuse is set, it fills shortfalls with already-used entities.
func pickN(avail []Candidate, used map[string]bool, allowReuse bool, n int) []string {
	var picked []string
	for _, c := range avail {
		if len(picked) >= n {
			break
		}
		if !used[c.EntityID] {
			picked = append(picked, c.EntityID)
		}
	}
	if allowReuse {
		for _, c := range avail {
			if len(picked) >= n {
				break
			}
			if used[c.EntityID] && !contains(picked, c.EntityID) {
				picked = append(picked, c.EntityID)
			}
		}
	}
	return picked
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
