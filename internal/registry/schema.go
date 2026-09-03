// Package registry implements Cloudpath plugin discovery, manifest validation,
// registry entries, digest verification and the plugins.lock contract.
package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
)

// SchemaValidator validates YAML/JSON values against a JSON Schema draft-07
// document. It intentionally implements only the keywords used by
// spec/plugin-manifest.schema.json: type, required, properties, const, enum and
// items. This keeps the CLI free of third-party schema dependencies.
type SchemaValidator struct {
	schema map[string]any
}

// NewSchemaValidator parses a JSON Schema document.
func NewSchemaValidator(data []byte) (*SchemaValidator, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var schema map[string]any
	if err := dec.Decode(&schema); err != nil {
		return nil, fmt.Errorf("parse JSON schema: %w", err)
	}
	if schema == nil {
		return nil, errors.New("JSON schema must be an object")
	}
	return &SchemaValidator{schema: schema}, nil
}

// Validate checks value against the loaded schema.
func (v *SchemaValidator) Validate(value any) error {
	if v == nil || v.schema == nil {
		return errors.New("schema validator is not initialized")
	}
	return validateValue(v.schema, value, "$")
}

func validateValue(schema map[string]any, value any, path string) error {
	if schema == nil {
		return nil
	}

	if rawType, ok := schema["type"]; ok {
		if !matchesType(rawType, value) {
			return fmt.Errorf("%s: expected type %v, got %T", path, rawType, value)
		}
	}

	if rawRequired, ok := schema["required"]; ok {
		required := toStrings(rawRequired)
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: required properties require an object", path)
		}
		for _, name := range required {
			if _, exists := obj[name]; !exists {
				return fmt.Errorf("%s: missing required property %q", path, name)
			}
		}
	}

	if rawProps, ok := schema["properties"]; ok {
		props, ok := rawProps.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: properties must be an object", path)
		}
		obj, ok := value.(map[string]any)
		if !ok {
			// The type keyword already reports non-object values when needed.
			return nil
		}
		for name, rawChild := range props {
			child, ok := rawChild.(map[string]any)
			if !ok {
				continue
			}
			if childValue, exists := obj[name]; exists {
				childPath := path + "/" + name
				if err := validateValue(child, childValue, childPath); err != nil {
					return err
				}
			}
		}
	}

	if rawConst, ok := schema["const"]; ok {
		if !valueEqual(value, rawConst) {
			return fmt.Errorf("%s: expected const %v, got %v", path, rawConst, value)
		}
	}

	if rawEnum, ok := schema["enum"]; ok {
		enums, ok := rawEnum.([]any)
		if !ok {
			return fmt.Errorf("%s: enum must be an array", path)
		}
		found := false
		for _, candidate := range enums {
			if valueEqual(value, candidate) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: value %v is not in enum %v", path, value, enums)
		}
	}

	if rawItems, ok := schema["items"]; ok {
		itemSchema, ok := rawItems.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: items must be a schema object", path)
		}
		arr, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: items requires an array", path)
		}
		for i, item := range arr {
			if err := validateValue(itemSchema, item, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	}

	return nil
}

func matchesType(rawType, value any) bool {
	switch typ := rawType.(type) {
	case string:
		return typeMatches(typ, value)
	case []any:
		for _, candidate := range typ {
			if s, ok := candidate.(string); ok && typeMatches(s, value) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func typeMatches(name string, value any) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		return isInteger(value)
	case "number":
		return isNumber(value)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func isNumber(v any) bool {
	switch n := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return !math.IsNaN(float64(n)) && !math.IsInf(float64(n), 0)
	case float64:
		return !math.IsNaN(n) && !math.IsInf(n, 0)
	case json.Number:
		_, err := n.Float64()
		return err == nil
	default:
		return false
	}
}

func isInteger(v any) bool {
	switch n := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return math.Trunc(float64(n)) == float64(n)
	case float64:
		return math.Trunc(n) == n
	case json.Number:
		_, err := n.Int64()
		return err == nil
	default:
		return false
	}
}

func valueEqual(a, b any) bool {
	if ai, ok := asInt64(a); ok {
		if bi, ok := asInt64(b); ok {
			return ai == bi
		}
	}
	if af, ok := asFloat64(a); ok {
		if bf, ok := asFloat64(b); ok {
			return af == bf
		}
	}
	return reflect.DeepEqual(a, b)
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case json.Number:
		num, err := n.Int64()
		return num, err == nil
	default:
		return 0, false
	}
}

func asFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		num, err := n.Float64()
		return num, err == nil
	default:
		return 0, false
	}
}

func toStrings(v any) []string {
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
