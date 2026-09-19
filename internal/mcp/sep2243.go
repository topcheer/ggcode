package mcp

// SEP-2243: HTTP Header Standardization for Streamable HTTP Transport
// (status: Final; introduced in the MCP 2026-07-28 spec). This file
// implements the CLIENT side: mirroring JSON-RPC routing fields into
// standard HTTP headers so network intermediaries (load balancers,
// proxies, rate limiters, observability) can route and inspect MCP
// traffic without deep packet inspection.
//
// Client obligations implemented here:
//  1. headerAnnotationViolations — validate x-mcp-header annotations in a
//     tool inputSchema; tools whose annotations violate the spec
//     constraints MUST be excluded from the tools/list result (a single
//     malformed tool definition must not prevent other valid tools from
//     being used) and SHOULD trigger a warning (debug.Log).
//  2. buildMCPParamHeaders — resolve x-mcp-header-annotated parameter
//     values from the call arguments (any nesting depth) and encode them
//     for Mcp-Param-{Name} headers. Null/absent parameters omit the
//     header per spec.
//  3. encodeMCPHeaderValue — value encoding rules (type conversion plus
//     the `=?base64?...?=` fallback) that prevent header injection and
//     preserve values the HTTP header grammar cannot represent.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	// mcpParamHeaderPrefix is the prefix for custom headers mirrored from
	// tool parameters annotated with x-mcp-header (SEP-2243).
	mcpParamHeaderPrefix = "Mcp-Param-"
	// base64SentinelPrefix/Suffix delimit a Base64-encoded header value:
	// `=?base64?{payload}?=`. The markers are case-sensitive per spec and
	// any plain value that would look like the sentinel must itself be
	// Base64-escaped to avoid ambiguity.
	base64SentinelPrefix = "=?base64?"
	base64SentinelSuffix = "?="
)

// headerTokenRe matches the RFC 9110 field-name token syntax (1*tchar):
// exactly the characters allowed in an HTTP header name.
var headerTokenRe = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)

// headerAnnotation is one resolved x-mcp-header annotation: the full
// HTTP header name to emit and the argument path of the parameter that
// carries the value.
type headerAnnotation struct {
	Header string
	Path   []string
}

// collectHeaderAnnotations walks schema (descending through "properties"
// and "items" at any nesting depth) collecting x-mcp-header annotations.
// When violations is non-nil, spec violations found along the way are
// appended there; the returned slice always contains only annotations
// that passed every constraint. Duplicate x-mcp-header values are
// compared case-insensitively (HTTP field names are case-insensitive).
func collectHeaderAnnotations(schema json.RawMessage, violations *[]string) []headerAnnotation {
	if len(schema) == 0 {
		return nil
	}
	var root interface{}
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil
	}
	var out []headerAnnotation
	seen := make(map[string]bool)
	var walk func(node interface{}, path []string)
	walk = func(node interface{}, path []string) {
		obj, ok := node.(map[string]interface{})
		if !ok {
			return
		}
		props, _ := obj["properties"].(map[string]interface{})
		for name, raw := range props {
			childPath := append(append([]string(nil), path...), name)
			prop, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if hv, ok := prop["x-mcp-header"]; ok {
				header, okStr := hv.(string)
				switch {
				case !okStr:
					violate(violations, "property %q: x-mcp-header must be a string", name)
				case header == "":
					violate(violations, "property %q: x-mcp-header must not be empty", name)
				case !headerTokenRe.MatchString(header):
					violate(violations, "property %q: x-mcp-header %q is not a valid HTTP header token", name, header)
				case seen[strings.ToLower(header)]:
					violate(violations, "property %q: duplicate x-mcp-header %q (case-insensitive)", name, header)
				default:
					seen[strings.ToLower(header)] = true
					out = append(out, headerAnnotation{Header: mcpParamHeaderPrefix + header, Path: childPath})
				}
				// Type constraint: annotations are only permitted on
				// primitive parameters (integer, string, boolean). number
				// is explicitly not permitted per SEP-2243.
				if badPrimitiveType(prop["type"]) {
					violate(violations, "property %q: x-mcp-header requires a primitive parameter type (integer, string, boolean)", name)
				}
			}
			walk(prop, childPath)
		}
		if items, ok := obj["items"]; ok {
			walk(items, path)
		}
	}
	walk(root, nil)
	return out
}

func violate(violations *[]string, format string, args ...interface{}) {
	if violations != nil {
		*violations = append(*violations, fmt.Sprintf(format, args...))
	}
}

// badPrimitiveType reports whether a JSON Schema "type" value (string or
// union array) includes a type on which x-mcp-header is not permitted.
func badPrimitiveType(t interface{}) bool {
	switch tt := t.(type) {
	case string:
		return tt == "number" || tt == "object" || tt == "array"
	case []interface{}:
		for _, e := range tt {
			if es, ok := e.(string); ok && (es == "number" || es == "object" || es == "array") {
				return true
			}
		}
	}
	return false
}

// headerAnnotationViolations returns the SEP-2243 constraint violations
// in a tool inputSchema, or nil when the schema is conformant.
func headerAnnotationViolations(schema json.RawMessage) []string {
	var violations []string
	collectHeaderAnnotations(schema, &violations)
	return violations
}

// buildMCPParamHeaders resolves the Mcp-Param-* headers for a tools/call
// request: for each valid x-mcp-header annotation in the tool's
// inputSchema, look the value up in args (any nesting depth), encode it,
// and emit header name/value pairs. Per spec: parameters that are null
// or absent omit the header; values that cannot be safely encoded are
// omitted so the server's -32001 path can trigger a schema refresh.
func buildMCPParamHeaders(schema json.RawMessage, args map[string]interface{}) [][2]string {
	annotations := collectHeaderAnnotations(schema, nil)
	if len(annotations) == 0 {
		return nil
	}
	var headers [][2]string
	for _, ann := range annotations {
		v, ok := lookupPath(args, ann.Path)
		if !ok || v == nil {
			continue
		}
		encoded, ok := encodeMCPHeaderValue(v)
		if !ok {
			continue
		}
		headers = append(headers, [2]string{ann.Header, encoded})
	}
	return headers
}

// lookupPath resolves a dotted argument path (e.g. ["options","region"])
// through nested maps.
func lookupPath(args map[string]interface{}, path []string) (interface{}, bool) {
	var cur interface{} = args
	for _, seg := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// encodeMCPHeaderValue converts a parameter value to its header string
// representation and applies the SEP-2243 encoding rules: strings as-is,
// booleans as lowercase true/false, integers as decimal strings (JSON
// numbers arrive as float64 and must be integral — fractional, NaN and
// Inf values are not representable and are dropped). Values that are not
// plain ASCII, contain control characters, start/end with whitespace, or
// would be ambiguous with the Base64 sentinel are Base64-escaped as
// `=?base64?{payload}?=` so servers and intermediaries can decode them.
func encodeMCPHeaderValue(v interface{}) (string, bool) {
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case bool:
		s = strconv.FormatBool(t)
	case float64:
		// Range check BEFORE the conversion: Go leaves out-of-range
		// float->int conversions implementation-dependent (amd64 yields
		// MinInt64, arm64 saturates to MaxInt64), so a large integral
		// float64 (e.g. 1e300) would silently encode as a garbage header
		// value instead of being dropped. Valid int64 range is
		// [-2^63, 2^63-1]; both bounds are exactly representable as
		// float64 powers of two, so the comparison itself cannot round.
		if math.IsNaN(t) || math.IsInf(t, 0) || t != math.Trunc(t) ||
			t < -(1<<63) || t >= 1<<63 {
			return "", false
		}
		s = strconv.FormatInt(int64(t), 10)
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return "", false
		}
		s = strconv.FormatInt(i, 10)
	case int:
		s = strconv.Itoa(t)
	case int32:
		s = strconv.FormatInt(int64(t), 10)
	case int64:
		s = strconv.FormatInt(t, 10)
	default:
		return "", false
	}
	return encodeHeaderText(s), true
}

// encodeHeaderText applies the SEP-2243 character rules to a converted
// string value: escape via Base64 when it starts/ends with whitespace,
// contains non-ASCII or control characters, or matches the Base64
// sentinel pattern (which would otherwise decode ambiguously).
func encodeHeaderText(s string) string {
	if len(s) == 0 {
		return s
	}
	if strings.HasPrefix(s, base64SentinelPrefix) && strings.HasSuffix(s, base64SentinelSuffix) {
		return base64HeaderValue(s)
	}
	if c := s[0]; c == ' ' || c == '\t' {
		return base64HeaderValue(s)
	}
	if c := s[len(s)-1]; c == ' ' || c == '\t' {
		return base64HeaderValue(s)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		// Tab (0x09) is treated as a control character here too: it is
		// legal in RFC 9110 field-values but ambiguous across
		// intermediaries, so escaping keeps SEP-2243 semantics strict.
		if c < 0x20 || c > 0x7E {
			return base64HeaderValue(s)
		}
	}
	return s
}

func base64HeaderValue(s string) string {
	return base64SentinelPrefix + base64.StdEncoding.EncodeToString([]byte(s)) + base64SentinelSuffix
}
