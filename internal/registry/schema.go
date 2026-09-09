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
	"regexp"
	"strings"
	"unicode/utf8"
)

// SchemaValidator validates YAML/JSON values against the JSON Schema subset used
// by CloudPath contract assets. It intentionally stays dependency-free, but
// supports local $ref/$defs, object/array/string/number bounds, patterns,
// additionalProperties, enums/consts and composition keywords used by the
// manifest and plugin UI schemas.
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
	return validateValue(v.schema, v.schema, value, "$", 0)
}

func validateValue(root, schema map[string]any, value any, path string, depth int) error {
	if schema == nil {
		return nil
	}
	if depth > 64 {
		return fmt.Errorf("%s: schema reference depth exceeded", path)
	}

	if rawRef, ok := schema["$ref"]; ok {
		ref, ok := rawRef.(string)
		if !ok {
			return fmt.Errorf("%s: $ref must be a string", path)
		}
		target, err := resolveSchemaRef(root, ref)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := validateValue(root, target, value, path, depth+1); err != nil {
			return err
		}
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
		if obj, ok := value.(map[string]any); ok {
			for name, rawChild := range props {
				child, ok := rawChild.(map[string]any)
				if !ok {
					continue
				}
				if childValue, exists := obj[name]; exists {
					if err := validateValue(root, child, childValue, path+"/"+name, depth+1); err != nil {
						return err
					}
				}
			}
			if rawAdditional, ok := schema["additionalProperties"]; ok {
				switch additional := rawAdditional.(type) {
				case bool:
					if !additional {
						for name := range obj {
							if _, declared := props[name]; !declared {
								return fmt.Errorf("%s: additional property %q is not allowed", path, name)
							}
						}
					}
				case map[string]any:
					for name, childValue := range obj {
						if _, declared := props[name]; declared {
							continue
						}
						if err := validateValue(root, additional, childValue, path+"/"+name, depth+1); err != nil {
							return err
						}
					}
				default:
					return fmt.Errorf("%s: additionalProperties must be a boolean or schema object", path)
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
			if err := validateValue(root, itemSchema, item, fmt.Sprintf("%s/%d", path, i), depth+1); err != nil {
				return err
			}
		}
	}

	if arr, ok := value.([]any); ok {
		if rawMin, exists := schema["minItems"]; exists {
			if min, ok := asInt64(rawMin); ok && int64(len(arr)) < min {
				return fmt.Errorf("%s: has %d items, minimum is %d", path, len(arr), min)
			}
		}
		if rawMax, exists := schema["maxItems"]; exists {
			if max, ok := asInt64(rawMax); ok && int64(len(arr)) > max {
				return fmt.Errorf("%s: has %d items, maximum is %d", path, len(arr), max)
			}
		}
		if unique, ok := schema["uniqueItems"].(bool); ok && unique {
			for i := 0; i < len(arr); i++ {
				for j := i + 1; j < len(arr); j++ {
					if reflect.DeepEqual(arr[i], arr[j]) {
						return fmt.Errorf("%s: items must be unique", path)
					}
				}
			}
		}
	}

	if str, ok := value.(string); ok {
		length := utf8.RuneCountInString(str)
		if rawMin, exists := schema["minLength"]; exists {
			if min, ok := asInt64(rawMin); ok && int64(length) < min {
				return fmt.Errorf("%s: length %d is below minimum %d", path, length, min)
			}
		}
		if rawMax, exists := schema["maxLength"]; exists {
			if max, ok := asInt64(rawMax); ok && int64(length) > max {
				return fmt.Errorf("%s: length %d exceeds maximum %d", path, length, max)
			}
		}
		if rawPattern, exists := schema["pattern"]; exists {
			pattern, ok := rawPattern.(string)
			if !ok {
				return fmt.Errorf("%s: pattern must be a string", path)
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("%s: invalid pattern: %w", path, err)
			}
			if !re.MatchString(str) {
				return fmt.Errorf("%s: value %q does not match pattern %q", path, str, pattern)
			}
		}
	}

	if num, ok := asFloat64(value); ok {
		if rawMin, exists := schema["minimum"]; exists {
			if min, ok := asFloat64(rawMin); ok && num < min {
				return fmt.Errorf("%s: value %v is below minimum %v", path, value, min)
			}
		}
		if rawMax, exists := schema["maximum"]; exists {
			if max, ok := asFloat64(rawMax); ok && num > max {
				return fmt.Errorf("%s: value %v exceeds maximum %v", path, value, max)
			}
		}
	}

	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		raw, exists := schema[keyword]
		if !exists {
			continue
		}
		children, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s: %s must be an array", path, keyword)
		}
		matches := 0
		var firstErr error
		for _, rawChild := range children {
			child, ok := rawChild.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: %s entries must be schema objects", path, keyword)
			}
			if err := validateValue(root, child, value, path, depth+1); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			matches++
		}
		switch keyword {
		case "allOf":
			if matches != len(children) {
				return firstErr
			}
		case "anyOf":
			if matches == 0 {
				return fmt.Errorf("%s: does not match anyOf: %v", path, firstErr)
			}
		case "oneOf":
			if matches != 1 {
				return fmt.Errorf("%s: matches %d schemas, want exactly one", path, matches)
			}
		}
	}

	if rawNot, ok := schema["not"].(map[string]any); ok {
		if err := validateValue(root, rawNot, value, path, depth+1); err == nil {
			return fmt.Errorf("%s: value must not match schema", path)
		}
	}

	return nil
}

func resolveSchemaRef(root map[string]any, ref string) (map[string]any, error) {
	if ref == "#" {
		return root, nil
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported $ref %q", ref)
	}
	var current any = root
	for _, rawPart := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(rawPart, "~1", "/"), "~0", "~")
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref %q does not resolve to an object", ref)
		}
		current, ok = obj[part]
		if !ok {
			return nil, fmt.Errorf("$ref %q not found", ref)
		}
	}
	target, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref %q does not resolve to a schema object", ref)
	}
	return target, nil
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
