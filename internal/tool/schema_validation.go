package tool

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ValidateSchemaConstraints checks tool arguments against the JSON Schema beyond
// just required fields. It validates:
//
//   - enum: value must be one of the allowed options
//   - minimum / maximum (exclusiveMinimum / exclusiveMaximum): numeric bounds
//   - minLength / maxLength: string length bounds
//
// Returns an empty string if all constraints pass, or a human-readable error
// message describing the first violation. This is called after CoerceArguments
// and ValidateRequiredParams in the agent's tool execution pipeline.
//
// Rationale: Weak models frequently send invalid enum values (e.g. "xyz" when
// the enum is ["read", "write"]) or out-of-range numbers. Without early
// validation, the tool either fails with a confusing error or silently
// misbehaves, wasting a full agent loop iteration. By catching schema
// violations before execution, the model gets a precise correction message and
// can fix the call on the next turn.
func ValidateSchemaConstraints(schema json.RawMessage, args json.RawMessage) string {
	if len(schema) == 0 || len(args) == 0 {
		return ""
	}

	var props map[string]json.RawMessage
	if err := json.Unmarshal(schema, &props); err != nil {
		return ""
	}
	propertiesRaw, ok := props["properties"]
	if !ok {
		return ""
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(propertiesRaw, &properties); err != nil {
		return ""
	}

	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {
		return ""
	}

	for fieldName, fieldSchema := range properties {
		val, exists := argMap[fieldName]
		if !exists || isEmptyValue(val) {
			continue // missing params are handled by ValidateRequiredParams
		}
		if msg := validateField(fieldName, fieldSchema, val); msg != "" {
			return msg
		}
	}

	return ""
}

// validateField checks a single argument value against its schema definition.
func validateField(fieldName string, fieldSchema, val json.RawMessage) string {
	var spec struct {
		Enum             []json.RawMessage `json:"enum"`
		Minimum          *float64          `json:"minimum"`
		Maximum          *float64          `json:"maximum"`
		ExclusiveMinimum *float64          `json:"exclusiveMinimum"`
		ExclusiveMaximum *float64          `json:"exclusiveMaximum"`
		MinLength        *int              `json:"minLength"`
		MaxLength        *int              `json:"maxLength"`
	}
	if err := json.Unmarshal(fieldSchema, &spec); err != nil {
		return ""
	}

	// Enum check
	if len(spec.Enum) > 0 {
		if !enumContains(spec.Enum, val) {
			allowed := make([]string, len(spec.Enum))
			for i, e := range spec.Enum {
				allowed[i] = strings.Trim(string(e), `"`)
			}
			return fmt.Sprintf("parameter %q must be one of [%s], got %s", fieldName, strings.Join(allowed, ", "), strings.TrimSpace(string(val)))
		}
	}

	// Numeric bounds (apply to integer and number types)
	if spec.Minimum != nil || spec.Maximum != nil || spec.ExclusiveMinimum != nil || spec.ExclusiveMaximum != nil {
		var numType struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(fieldSchema, &numType)
		if numType.Type == "integer" || numType.Type == "number" {
			n, ok := parseJSONNumber(val)
			if ok {
				if spec.Minimum != nil && n < *spec.Minimum {
					return fmt.Sprintf("parameter %q must be >= %v, got %v", fieldName, *spec.Minimum, n)
				}
				if spec.ExclusiveMinimum != nil && n <= *spec.ExclusiveMinimum {
					return fmt.Sprintf("parameter %q must be > %v, got %v", fieldName, *spec.ExclusiveMinimum, n)
				}
				if spec.Maximum != nil && n > *spec.Maximum {
					return fmt.Sprintf("parameter %q must be <= %v, got %v", fieldName, *spec.Maximum, n)
				}
				if spec.ExclusiveMaximum != nil && n >= *spec.ExclusiveMaximum {
					return fmt.Sprintf("parameter %q must be < %v, got %v", fieldName, *spec.ExclusiveMaximum, n)
				}
			}
		}
	}

	// String length bounds
	if spec.MinLength != nil || spec.MaxLength != nil {
		var numType struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(fieldSchema, &numType)
		if numType.Type == "string" {
			var s string
			if err := json.Unmarshal(val, &s); err == nil {
				if spec.MinLength != nil && len(s) < *spec.MinLength {
					return fmt.Sprintf("parameter %q must be at least %d characters, got %d", fieldName, *spec.MinLength, len(s))
				}
				if spec.MaxLength != nil && len(s) > *spec.MaxLength {
					return fmt.Sprintf("parameter %q must be at most %d characters, got %d", fieldName, *spec.MaxLength, len(s))
				}
			}
		}
	}

	return ""
}

// enumContains checks if a value matches any entry in the enum list.
func enumContains(enum []json.RawMessage, val json.RawMessage) bool {
	normalized := strings.TrimSpace(string(val))
	for _, e := range enum {
		if strings.TrimSpace(string(e)) == normalized {
			return true
		}
	}
	return false
}

// parseJSONNumber parses a JSON number from RawMessage, returning false if
// the value is not a number.
func parseJSONNumber(val json.RawMessage) (float64, bool) {
	s := strings.TrimSpace(string(val))
	if s == "" || s[0] == '"' {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// StripUnknownParams removes arguments that are not declared in the tool's
// schema properties. Weak models sometimes hallucinate extra parameters (e.g.
// sending {"path": "...", "recursive": true} to a tool that has no "recursive"
// parameter). While Go's json.Unmarshal silently ignores unknown fields in most
// cases, some tools use strict unmarshalling or map-based deserialization where
// unexpected keys cause failures or misbehavior.
//
// Returns the cleaned arguments JSON. If the schema has no properties or the
// arguments can't be parsed, the original input is returned unchanged.
func StripUnknownParams(schema json.RawMessage, args json.RawMessage) json.RawMessage {
	if len(schema) == 0 || len(args) == 0 {
		return args
	}

	var props map[string]json.RawMessage
	if err := json.Unmarshal(schema, &props); err != nil {
		return args
	}
	propertiesRaw, ok := props["properties"]
	if !ok {
		return args // no properties → can't determine what's known
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(propertiesRaw, &properties); err != nil {
		return args
	}

	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {
		return args
	}

	// Check if additionalProperties is explicitly true (allow extras)
	if ap, ok := props["additionalProperties"]; ok {
		var allow bool
		if err := json.Unmarshal(ap, &allow); err == nil && allow {
			return args
		}
	}

	changed := false
	for key := range argMap {
		if _, known := properties[key]; !known {
			delete(argMap, key)
			changed = true
		}
	}

	if !changed {
		return args
	}

	out, err := json.Marshal(argMap)
	if err != nil {
		return args
	}
	return out
}
