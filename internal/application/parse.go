package application

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// requirementsFile mirrors the machine manifest shape:
//
//	requirements:
//	  - id: reminder-output
//	    capability: cloudpath.dev/capability/alarm@1
//	    cardinality: one
type requirementsFile struct {
	Requirements []Requirement `yaml:"requirements"`
}

// ParseRequirements decodes a manifest's requirement list. It accepts either a
// document with a top-level "requirements" key (plugin.yaml / requirements.yaml)
// or a bare YAML sequence of requirement objects. Declared requirements are
// validated; an invalid declaration returns an error.
func ParseRequirements(data []byte) ([]Requirement, error) {
	var file requirementsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse requirements: %w", err)
	}
	if len(file.Requirements) > 0 {
		if err := validateRequirementList(file.Requirements); err != nil {
			return nil, err
		}
		return file.Requirements, nil
	}
	var list []Requirement
	if err := yaml.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse requirements list: %w", err)
	}
	if err := validateRequirementList(list); err != nil {
		return nil, err
	}
	return list, nil
}

// validateRequirementList checks the declared requirements: unique IDs, valid
// cardinality, parseable capability and sane minItems.
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
