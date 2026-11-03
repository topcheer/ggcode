package tool

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

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
			continue
		}
		if msg := validateField(fieldName, fieldSchema, val); msg != "" {
			return msg
		}
	}

	return ""
}

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

	if len(spec.Enum) > 0 {
		if !enumContains(spec.Enum, val) {
			allowed := make([]string, len(spec.Enum))
			for i, e := range spec.Enum {
				allowed[i] = strings.Trim(string(e), `"`)
			}
			msg := fmt.Sprintf("parameter %q must be one of [%s], got %s", fieldName, strings.Join(allowed, ", "), strings.TrimSpace(string(val)))

			provided := strings.TrimSpace(strings.Trim(string(val), `"`))
			if hint := suggestClosestEnum(provided, spec.Enum); hint != "" {
				msg += fmt.Sprintf(". Did you mean %q?", hint)
			}
			return msg
		}
	}

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

	if spec.MinLength != nil || spec.MaxLength != nil {
		var numType struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(fieldSchema, &numType)
		if numType.Type == "string" {
			var s string
			if err := json.Unmarshal(val, &s); err == nil {

				rlen := utf8.RuneCountInString(s)
				if spec.MinLength != nil && rlen < *spec.MinLength {
					return fmt.Sprintf("parameter %q must be at least %d characters, got %d", fieldName, *spec.MinLength, rlen)
				}
				if spec.MaxLength != nil && rlen > *spec.MaxLength {
					return fmt.Sprintf("parameter %q must be at most %d characters, got %d", fieldName, *spec.MaxLength, rlen)
				}
			}
		}
	}

	return ""
}

func enumContains(enum []json.RawMessage, val json.RawMessage) bool {
	normalized := strings.TrimSpace(string(val))
	for _, e := range enum {
		if strings.TrimSpace(string(e)) == normalized {
			return true
		}
	}
	return false
}

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
		return args
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(propertiesRaw, &properties); err != nil {
		return args
	}

	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {
		return args
	}

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
