package application

// Binder carries the tenant scope and instance identity for a binding
// operation. Matching uses Capability, tenant and the Core-only provider
// identity. It never couples the Application Protocol to a Driver.
type Binder struct {
	ApplicationID    string
	PluginInstanceID string
	TenantID         string
}

// Match auto-selects a candidate for every requirement within the binder's
// tenant scope. The returned binding retains Candidate.DeviceID so later
// command effects can be routed to the exact provider selected here.
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
		for _, candidate := range picked {
			bindings = append(bindings, Binding{
				RequirementID: req.ID,
				EntityID:      candidate.EntityID,
				DeviceID:      candidate.DeviceID,
			})
			used[candidateTargetKey(candidate.DeviceID, candidate.EntityID)] = true
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

// matchRequirement resolves one requirement to candidate providers. It picks
// unused targets first, and only falls back to already-used targets when the
// requirement allows reuse.
func matchRequirement(req Requirement, candidates []Candidate, used map[string]bool) ([]Candidate, error) {
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
		candidate, ok := firstUnused(avail, used, req.AllowReuse)
		if !ok {
			return nil, &BindingError{Result: duplicateOccupationResult(req, "")}
		}
		return []Candidate{candidate}, nil
	case CardinalityOne:
		if len(avail) == 0 {
			return nil, &BindingError{Result: missingRequiredResult(req)}
		}
		candidate, ok := firstUnused(avail, used, req.AllowReuse)
		if !ok {
			return nil, &BindingError{Result: duplicateOccupationResult(req, "")}
		}
		return []Candidate{candidate}, nil
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

// firstUnused returns the first candidate target that is not occupied, or,
// when allowReuse is set, the first candidate regardless of occupation.
func firstUnused(avail []Candidate, used map[string]bool, allowReuse bool) (Candidate, bool) {
	for _, c := range avail {
		if !used[candidateTargetKey(c.DeviceID, c.EntityID)] {
			return c, true
		}
	}
	if allowReuse && len(avail) > 0 {
		return avail[0], true
	}
	return Candidate{}, false
}

// pickN selects up to n distinct candidate targets, preferring unused ones.
// If allowReuse is set, it fills shortfalls with already-used targets.
func pickN(avail []Candidate, used map[string]bool, allowReuse bool, n int) []Candidate {
	var picked []Candidate
	seen := map[string]bool{}
	for _, c := range avail {
		if len(picked) >= n {
			break
		}
		key := candidateTargetKey(c.DeviceID, c.EntityID)
		if !used[key] && !seen[key] {
			picked = append(picked, c)
			seen[key] = true
		}
	}
	if allowReuse {
		for _, c := range avail {
			if len(picked) >= n {
				break
			}
			key := candidateTargetKey(c.DeviceID, c.EntityID)
			if used[key] && !seen[key] {
				picked = append(picked, c)
				seen[key] = true
			}
		}
	}
	return picked
}

func candidateTargetKey(deviceID, entityID string) string {
	if deviceID == "" {
		return entityID
	}
	return deviceID + "\x00" + entityID
}
