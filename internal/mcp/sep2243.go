package mcp

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
	mcpParamHeaderPrefix = "Mcp-Param-"

	base64SentinelPrefix = "=?base64?"
	base64SentinelSuffix = "?="
)

var headerTokenRe = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)

type headerAnnotation struct {
	Header string
	Path   []string
}

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
	var walk func(node interface{}, path []string, underArray bool)
	walk = func(node interface{}, path []string, underArray bool) {
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
					if underArray {

						violate(violations, "property %q: x-mcp-header inside array items is not supported (array element has no single value)", name)
						continue
					}
					out = append(out, headerAnnotation{Header: mcpParamHeaderPrefix + header, Path: childPath})
				}

				if badPrimitiveType(prop["type"]) {
					violate(violations, "property %q: x-mcp-header requires a primitive parameter type (integer, string, boolean)", name)
				}
			}
			walk(prop, childPath, underArray)
		}
		if items, ok := obj["items"]; ok {

			walk(items, path, true)
		}
	}
	walk(root, nil, false)
	return out
}

func violate(violations *[]string, format string, args ...interface{}) {
	if violations != nil {
		*violations = append(*violations, fmt.Sprintf(format, args...))
	}
}

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

func headerAnnotationViolations(schema json.RawMessage) []string {
	var violations []string
	collectHeaderAnnotations(schema, &violations)
	return violations
}

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

func encodeMCPHeaderValue(v interface{}) (string, bool) {
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case bool:
		s = strconv.FormatBool(t)
	case float64:

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

		if c < 0x20 || c > 0x7E {
			return base64HeaderValue(s)
		}
	}
	return s
}

func base64HeaderValue(s string) string {
	return base64SentinelPrefix + base64.StdEncoding.EncodeToString([]byte(s)) + base64SentinelSuffix
}
