package tool

import (
	"encoding/json"
	"fmt"
	"math"
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
	case "array":
		return coerceArrayValue(fieldSchema, val)
	case "object":
		return coerceObjectValue(fieldSchema, val)
	default:
		return val, false
	}
}

// coerceArrayValue recursively coerces each element of a JSON array against
// the schema's "items" declaration (#568). Coercion previously stopped at
// the top level, so MCP tool schemas declaring arrays of objects kept
// string-encoded numbers/booleans from weak models unconverted.
func coerceArrayValue(fieldSchema, val json.RawMessage) (json.RawMessage, bool) {
	s := strings.TrimSpace(string(val))
	if s == "" || s == "null" || s[0] != '[' {
		return val, false // not an array — nothing to coerce
	}
	var spec struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(fieldSchema, &spec); err != nil || len(spec.Items) == 0 {
		return val, false // no item schema — leave alone
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

// coerceObjectValue recursively coerces the properties of a JSON object
// against the schema's "properties" declarations (#568).
func coerceObjectValue(fieldSchema, val json.RawMessage) (json.RawMessage, bool) {
	s := strings.TrimSpace(string(val))
	if s == "" || s == "null" || s[0] != '{' {
		return val, false // not an object — nothing to coerce
	}
	var spec struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(fieldSchema, &spec); err != nil || len(spec.Properties) == 0 {
		return val, false // no property schemas — leave alone
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
	// #4376: float-form integer literals ("9007199254740993.0", "...e0")
	// lose precision through the float64 round-trip below — 2^53+1 silently
	// becomes 2^53. Rewrite them to exact plain decimal first.
	if d := exactDecimal(s); d != "" {
		return json.RawMessage(d), true
	}
	// Tolerate "42.0" — models sometimes send floats for integer fields.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		// Guard against overflow: converting out-of-range floats (e.g. "1e300")
		// to int64 is undefined in Go and would silently produce garbage.
		// Reject the coercion so downstream reports a type error (#261).
		// #296: math.MaxInt64 (2^63-1) converts to float64 as exactly 2^63,
		// so `>` let 2^63 through — use >= to reject it too. NaN compares
		// false with everything and must be rejected explicitly.
		if math.IsNaN(f) || f < math.MinInt64 || f >= math.MaxInt64 {
			return val, false
		}
		// #802: only INTEGRAL floats may coerce ('42.0' -> 42). int64(f)
		// silently truncated '3.7' -> 3, executing a weak model's typo'd
		// integer param (lines/offset/counts) with wrong data instead of the
		// corrective type error this module's philosophy (L26) promises.
		if f != math.Trunc(f) {
			return val, false
		}
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
	// #4376: integer-valued strings must bypass the float64 round-trip —
	// ParseFloat+FormatFloat silently turns 2^53+1 into 2^53 for MCP tools
	// whose schema declares "number" but whose values are large integer IDs.
	if d := exactDecimal(s); d != "" {
		return json.RawMessage(d), true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return val, false
	}
	// #3109: ParseFloat accepts "inf"/"Infinity"/"nan" (case-insensitive)
	// with a nil error; FormatFloat would then emit "+Inf"/"NaN" - not
	// legal JSON tokens - into the RawMessage, and downstream json.Unmarshal
	// fails with an opaque parse error that never names the offending field.
	// Reject non-finite values so the caller keeps the original value and
	// reports a clear type error, mirroring the coerceInteger NaN guard.
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return val, false
	}
	return json.RawMessage(strconv.FormatFloat(f, 'f', -1, 64)), true
}

// exactDecimal rewrites an integer-valued decimal literal — including
// float forms like "42.0", "1e3" or "9007199254740993.0" — into its exact
// plain-decimal representation, so callers can ParseInt without a lossy
// float64 round-trip (#4376). Returns "" when s is not such a literal or
// the exact value is out of int64 range (callers then fall back to float,
// preserving pre-existing behavior for those inputs).
func exactDecimal(s string) string {
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	// Optional leading + ("+42.0") is legal for strconv.ParseFloat and must
	// be handled here too or such strings silently skip the exact path.
	s = strings.TrimPrefix(s, "+")
	if s == "" {
		return ""
	}
	mant, exp := s, 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant = s[:i]
		e, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return ""
		}
		exp = e
	}
	intPart, fracPart := mant, ""
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		intPart, fracPart = mant[:i], mant[i+1:]
	}
	if intPart == "" || !allDigits(intPart) || !allDigits(fracPart) {
		return ""
	}
	digits := intPart + fracPart
	point := len(intPart) + exp // decimal point position within digits
	if point < 0 || point > len(digits)+20 {
		// point < 0: value < 1 (only integral if all zeros — reject, callers'
		// float path with its integrality guard handles absurd "0e-5" inputs
		// exactly as before). Cap exponent expansion to keep this O(1)-ish.
		return ""
	}
	if point < len(digits) && !allZero(digits[point:]) {
		return "" // non-integral (e.g. "3.14", "1.5e1") — not our case
	}
	for len(digits) < point {
		digits += "0"
	}
	out := digits[:point]
	if out == "" {
		return ""
	}
	if neg {
		out = "-" + out
	}
	// Exact range check: ParseInt both validates and is the caller's parse.
	if _, err := strconv.ParseInt(out, 10, 64); err != nil {
		return ""
	}
	return out
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func allZero(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return false
		}
	}
	return true
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

// ValidateRequiredParams checks that all fields listed in the schema's
// "required" array are present and non-empty in the provided arguments.
// Returns an empty string if all required fields are satisfied, or a
// user-friendly error message listing the missing fields.
//
// This runs BEFORE tool execution to give the model a clear, actionable
// error message ("missing required parameter: pattern") instead of a
// confusing downstream failure (grep with empty pattern, unmarshal error,
// or silently wrong results). It complements CoerceArguments: coercion
// fixes types, validation catches omissions.
//
// A field is considered "missing" when it is absent from the arguments JSON,
// null, or an empty/whitespace-only string. An explicit empty array [] or
// object {} counts as provided — it carries meaning (e.g. clearing a list).
// Numeric and boolean values (including 0 and false) are always considered
// present.
// emptyStringLegalParams lists (tool, param) pairs where an EXPLICIT empty
// string is a meaningful provided value, not an omission:
//   - edit_file new_text: "" is the DELETE semantics - the handler's own
//     CheckRequired deliberately omits new_text for exactly this reason
//     (#742 presence-only decision). The blanket empty=missing rule (#542)
//     silently overrode it upstream: every delete edit was rejected with
//     "missing required parameter: new_text" and the suggested remedy
//     (provide new_text) is unsatisfiable for a deletion - futile retry
//     loop (#568 family) or a risky full-file rewrite instead (#1838).
//   - write_file content: "" creates an empty file (legitimate).
//   - batch_replace replacement: "" deletes the matches.
//
// Absence and null are still missing; only a PRESENT "" is accepted.
var emptyStringLegalParams = map[string]map[string]bool{
	"edit_file":     {"new_text": true},
	"write_file":    {"content": true},
	"batch_replace": {"replacement": true},
}

// emptyStringIsLegal reports whether tool/param accepts an explicit "".
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
		return "" // no required fields declared
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
		// Can't parse args — let the tool's own unmarshal produce the error.
		return ""
	}

	var missing []string
	for _, field := range requiredFields {
		val, exists := argMap[field]
		if !exists || isEmptyValue(val) {
			// #1838: presence-only for params whose explicit "" is legal
			// (delete semantics) - a PRESENT empty string passes; absence
			// and null still fail.
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

// isEmptyValue returns true if a JSON RawMessage represents an empty value:
// null, empty string "", or a whitespace-only string.
//
// Explicit empty arrays [] and objects {} are NOT empty — they are provided
// values with meaning (todo_write {"todos":[]} legitimately clears the list;
// flagging them missing made the model retry the same call in a loop, #568).
// Numbers (including 0) and booleans (including false) are NOT empty.
// Whitespace-only strings (" ", "\t") are empty (#542) — consistent with
// CheckRequired's Trim behavior in tool.go, they must not slip past the
// required-param gateway.
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
		// Malformed quoted value — treat as non-empty so validation stays neutral.
	}
	return false
}

// isNullValue reports whether the raw JSON value is null.
func isNullValue(val json.RawMessage) bool {
	return strings.TrimSpace(string(val)) == "null"
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
