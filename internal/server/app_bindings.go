package server

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/DeliciousBuding/cloud-path/internal/application"
)

// appBindingsKey is an optional complete selection of stable entities. Keeping
// it outside app_config prevents applications from learning device identities
// through their business configuration; they receive normal validated bindings.
const appBindingsKey = "app_bindings"

func selectApplicationBindings(binder application.Binder, requirements []application.Requirement, candidates []application.Candidate, configJSON string) (application.BindingSet, error) {
	var config map[string]string
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return application.BindingSet{}, fmt.Errorf("decode instance config: %w", err)
	}
	raw, explicit := config[appBindingsKey]
	if !explicit {
		return binder.Match(requirements, candidates)
	}
	var bindings []application.Binding
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bindings); err != nil {
		return application.BindingSet{}, fmt.Errorf("decode app_bindings: %w", err)
	}
	if bindings == nil || len(bindings) > 64 {
		return application.BindingSet{}, fmt.Errorf("app_bindings must be an array with at most 64 bindings")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return application.BindingSet{}, fmt.Errorf("app_bindings must contain one JSON array")
	}
	if result := binder.Validate(requirements, candidates, bindings); !result.Valid {
		return application.BindingSet{}, &application.BindingError{Result: result}
	}
	return application.BindingSet{ApplicationID: binder.ApplicationID, PluginInstanceID: binder.PluginInstanceID, TenantID: binder.TenantID, Bindings: bindings}, nil
}
