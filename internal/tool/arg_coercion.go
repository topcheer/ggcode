package tool

import (
	"encoding/json"
	"strconv"
	"strings"
)

// CoerceArguments applies schema-aware type coercion to a tool's JSON arguments.
// Weak models (especially open-weight models served via goolm, third-party
// endpoints) frequently emit string values where the schema expects integer,
// number, or boolean — e.g. {"offset": "50"} or {"headless": "true"}. The
// standard json.Unmarshal then fails with a type error, wasting an entire
// agent loop iteration.
//
// This function re-encodes the arguments JSON so that fields declared as
// integer/number in the schema receive numeric values, and fields declared as
// boolean receive bool values — regardless of whether the model sent them as
// strings. It is a no-op if the arguments are already well-typed or if the
// schema cannot be parsed.
//
// Returns the (possibly rewritten) arguments JSON. On any error the original
// input is returned unchanged so the tool's own unmarshal produces the same
// error it would have without coercion.
func CoerceArguments(schema json.RawMessage, args json.RawMessage) json.RawMessage {
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

	changed := false
	for fieldName, fieldSchema := range properties {
		val, exists := argMap[fieldName]
		if !exists {
			continue
		}
		coerced, ok := coerceValue(fieldSchema, val)
		if ok {
			argMap[fieldName] = coerced
			changed = true
		}
	}

	if !changed {
		return args // avoid re-encoding if nothing changed
	}
	out, err := json.Marshal(argMap)
	if err != nil {
		return args
	}
	return out
}

// coerceValue attempts to convert a single argument value to match the type
// declared in fieldSchema. Returns the coerced JSON value and true if a
// conversion was performed.
func coerceValue(fieldSchema, val json.RawMessage) (json.RawMessage, bool) {
	var spec struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(fieldSchema, &spec); err != nil {
		return val, false
	}

	switch spec.Type {
	case "integer":
		return coerceInteger(val)
	case "number":
		return coerceNumber(val)
	case "boolean":
		return coerceBoolean(val)
	default:
		return val, false
	}
}

// coerceInteger converts string-encoded integers to actual integers.
// "42" → 42, but also tolerates "42.0" (float-as-string from some models).
func coerceInteger(val json.RawMessage) (json.RawMessage, bool) {
	if isJSONNumber(val) || isJSONBool(val) {
		return val, false // already numeric or bool — leave alone
	}
	s := strings.TrimSpace(strings.Trim(string(val), `"`))
	if s == "" {
		return val, false
	}
	// Try integer parse first.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return json.RawMessage(strconv.FormatInt(n, 10)), true
	}
	// Tolerate "42.0" — models sometimes send floats for integer fields.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return json.RawMessage(strconv.FormatInt(int64(f), 10)), true
	}
	return val, false
}

// coerceNumber converts string-encoded numbers (int or float) to actual numbers.
func coerceNumber(val json.RawMessage) (json.RawMessage, bool) {
	if isJSONNumber(val) || isJSONBool(val) {
		return val, false
	}
	s := strings.TrimSpace(strings.Trim(string(val), `"`))
	if s == "" {
		return val, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return val, false
	}
	return json.RawMessage(strconv.FormatFloat(f, 'f', -1, 64)), true
}

// coerceBoolean converts string-encoded booleans to actual booleans.
// Accepts "true"/"false", "1"/"0", "yes"/"no".
func coerceBoolean(val json.RawMessage) (json.RawMessage, bool) {
	if isJSONBool(val) {
		return val, false
	}
	s := strings.ToLower(strings.TrimSpace(strings.Trim(string(val), `"`)))
	switch s {
	case "true", "1", "yes":
		return json.RawMessage("true"), true
	case "false", "0", "no":
		return json.RawMessage("false"), true
	default:
		return val, false
	}
}

// isJSONNumber returns true if val is a JSON number (not a string).
func isJSONNumber(val json.RawMessage) bool {
	s := strings.TrimSpace(string(val))
	if s == "" {
		return false
	}
	// JSON numbers start with a digit or minus sign and contain no quotes.
	if s[0] == '"' {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// isJSONBool returns true if val is a JSON boolean literal.
func isJSONBool(val json.RawMessage) bool {
	s := strings.TrimSpace(string(val))
	return s == "true" || s == "false"
}
