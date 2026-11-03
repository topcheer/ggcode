package tool

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

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
		return args
	}
	out, err := json.Marshal(argMap)
	if err != nil {
		return args
	}
	return out
}

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
	case "array":
		return coerceArrayValue(fieldSchema, val)
	case "object":
		return coerceObjectValue(fieldSchema, val)
	default:
		return val, false
	}
}

func coerceArrayValue(fieldSchema, val json.RawMessage) (json.RawMessage, bool) {
	s := strings.TrimSpace(string(val))
	if s == "" || s == "null" || s[0] != '[' {
		return val, false
	}
	var spec struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(fieldSchema, &spec); err != nil || len(spec.Items) == 0 {
		return val, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(val, &items); err != nil {
		return val, false
	}
	changed := false
	for i, item := range items {
		if coerced, ok := coerceValue(spec.Items, item); ok {
			items[i] = coerced
			changed = true
		}
	}
	if !changed {
		return val, false
	}
	out, err := json.Marshal(items)
	if err != nil {
		return val, false
	}
	return out, true
}

func coerceObjectValue(fieldSchema, val json.RawMessage) (json.RawMessage, bool) {
	s := strings.TrimSpace(string(val))
	if s == "" || s == "null" || s[0] != '{' {
		return val, false
	}
	var spec struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(fieldSchema, &spec); err != nil || len(spec.Properties) == 0 {
		return val, false
	}
	var objMap map[string]json.RawMessage
	if err := json.Unmarshal(val, &objMap); err != nil {
		return val, false
	}
	changed := false
	for fieldName, propSchema := range spec.Properties {
		existing, ok := objMap[fieldName]
		if !ok {
			continue
		}
		if coerced, done := coerceValue(propSchema, existing); done {
			objMap[fieldName] = coerced
			changed = true
		}
	}
	if !changed {
		return val, false
	}
	out, err := json.Marshal(objMap)
	if err != nil {
		return val, false
	}
	return out, true
}

func coerceInteger(val json.RawMessage) (json.RawMessage, bool) {
	if isJSONNumber(val) || isJSONBool(val) {
		return val, false
	}
	s := strings.TrimSpace(strings.Trim(string(val), `"`))
	if s == "" {
		return val, false
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return json.RawMessage(strconv.FormatInt(n, 10)), true
	}

	if f, err := strconv.ParseFloat(s, 64); err == nil {

		if math.IsNaN(f) || f < math.MinInt64 || f >= math.MaxInt64 {
			return val, false
		}

		if f != math.Trunc(f) {
			return val, false
		}

		if f >= (1<<53) || f <= -(1<<53) {
			return val, false
		}
		return json.RawMessage(strconv.FormatInt(int64(f), 10)), true
	}
	return val, false
}

func coerceNumber(val json.RawMessage) (json.RawMessage, bool) {
	if isJSONNumber(val) || isJSONBool(val) {
		return val, false
	}
	s := strings.TrimSpace(strings.Trim(string(val), `"`))
	if s == "" {
		return val, false
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return json.RawMessage(strconv.FormatInt(n, 10)), true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return val, false
	}

	if math.IsInf(f, 0) || math.IsNaN(f) {
		return val, false
	}
	return json.RawMessage(strconv.FormatFloat(f, 'f', -1, 64)), true
}

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

var emptyStringLegalParams = map[string]map[string]bool{
	"edit_file":     {"new_text": true},
	"write_file":    {"content": true},
	"batch_replace": {"replacement": true},
}

func emptyStringIsLegal(toolName, param string) bool {
	if toolName == "" {
		return false
	}
	m, ok := emptyStringLegalParams[toolName]
	return ok && m[param]
}

func ValidateRequiredParams(schema json.RawMessage, args json.RawMessage, toolName string) string {
	if len(schema) == 0 || len(args) == 0 {
		return ""
	}

	var props map[string]json.RawMessage
	if err := json.Unmarshal(schema, &props); err != nil {
		return ""
	}
	requiredRaw, ok := props["required"]
	if !ok {
		return ""
	}
	var requiredFields []string
	if err := json.Unmarshal(requiredRaw, &requiredFields); err != nil {
		return ""
	}
	if len(requiredFields) == 0 {
		return ""
	}

	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {

		return ""
	}

	var missing []string
	for _, field := range requiredFields {
		val, exists := argMap[field]
		if !exists || isEmptyValue(val) {

			if exists && emptyStringIsLegal(toolName, field) && !isNullValue(val) {
				continue
			}
			missing = append(missing, field)
		}
	}

	if len(missing) == 0 {
		return ""
	}
	if len(missing) == 1 {
		return fmt.Sprintf("missing required parameter: %s", missing[0])
	}
	return fmt.Sprintf("missing required parameters: %s", strings.Join(missing, ", "))
}

func isEmptyValue(val json.RawMessage) bool {
	s := strings.TrimSpace(string(val))
	if s == "" || s == "null" {
		return true
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if err := json.Unmarshal([]byte(s), &str); err == nil {
			return strings.TrimSpace(str) == ""
		}

	}
	return false
}

func isNullValue(val json.RawMessage) bool {
	return strings.TrimSpace(string(val)) == "null"
}

func isJSONNumber(val json.RawMessage) bool {
	s := strings.TrimSpace(string(val))
	if s == "" {
		return false
	}

	if s[0] == '"' {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func isJSONBool(val json.RawMessage) bool {
	s := strings.TrimSpace(string(val))
	return s == "true" || s == "false"
}
