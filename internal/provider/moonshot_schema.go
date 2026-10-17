package provider

import "encoding/json"

// Moonshot (Kimi) flavored-schema normalization (#4276).
//
// The moonshot API rejects tool parameter schemas it considers recursive
// without a termination condition; its live error shape is:
//
//	tools.function.parameters is not a valid moonshot flavored json schema,
//	details: <At path 'properties.steps': detected infinite recursion
//	without termination condition>
//
// Root cause: ggcode ships several tools whose parameters contain FREE-FORM
// object nodes - {"type":"object"} with neither "properties" nor
// "additionalProperties" (e.g. define_tool/macro steps[].args, which hold
// arbitrary per-tool arguments). The moonshot validator treats an
// unconstrained object as permitting unbounded nesting depth, i.e. infinite
// recursion with no termination. The official OpenAI endpoint accepts the
// same schema, so the divergence only bites moonshot-flavored endpoints.
//
// Fix strategy (decided in #4276, vendor-gated so no other endpoint sees a
// byte of difference): when the request targets a moonshot backend, walk
// every tool schema and give each free-form object node an explicit
// "additionalProperties": true. Declaring the extension point explicitly is
// the standard JSON Schema way to terminate the "anything may nest here"
// ambiguity the validator complains about.

// moonshotSchemaGateActive reports whether the request targets a moonshot
// backend. Dual-axis OR detection (#4276 final analysis):
//   - URL axis: base URL host contains moonshot.cn / moonshot.ai / kimi.com
//     (covers api.moonshot.cn and the kimi.com platform migration)
//   - model-name axis: the model id references kimi/moonshot (covers custom
//     vendor names and gateways that proxy moonshot models under arbitrary
//     base URLs - moonshot only serves its own models, so a moonshot model
//     id behind any URL is a moonshot backend)
//
// Vendor-name matching is deliberately NOT an axis: a custom-named vendor
// entry with a non-moonshot URL and non-moonshot model is not a moonshot
// backend, and the URL/model axes already cover every real case.
func moonshotSchemaGateActive(baseURL, model string) bool {
	if moonshotHostHint(baseURL) {
		return true
	}
	m := lowerASCII(model)
	return containsASCII(m, "kimi") || containsASCII(m, "moonshot")
}

func moonshotHostHint(rawURL string) bool {
	u := lowerASCII(rawURL)
	return containsASCII(u, "moonshot.cn") ||
		containsASCII(u, "moonshot.ai") ||
		containsASCII(u, "kimi.com")
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func containsASCII(s, sub string) bool {
	n := len(sub)
	if n == 0 || len(s) < n {
		return false
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return true
		}
	}
	return false
}

// normalizeMoonshotSchema returns params with every free-form object node
// ({"type":"object"} lacking both "properties" and "additionalProperties")
// given an explicit "additionalProperties": true. It is idempotent and never
// narrows any schema: nodes that already declare properties or an
// additionalProperties policy (including false) are left untouched.
func normalizeMoonshotSchema(params json.RawMessage) json.RawMessage {
	if len(params) == 0 || !json.Valid(params) {
		return params
	}
	var doc any
	if err := json.Unmarshal(params, &doc); err != nil {
		return params
	}
	if walkMoonshotSchema(doc) {
		out, err := json.Marshal(doc)
		if err == nil {
			return out
		}
	}
	return params
}

// walkMoonshotSchema recursively normalizes schema nodes and reports whether
// anything changed. It recurses into every map value and array element: in
// JSON Schema the map values under "properties"/"$defs" are name->schema
// containers (not schemas themselves) and the composition keys ("items",
// "anyOf", ...) hold schemas or schema arrays, so blanket recursion reaches
// every schema node at any depth while scalar values no-op.
func walkMoonshotSchema(node any) bool {
	switch v := node.(type) {
	case map[string]any:
		changed := false
		t, _ := v["type"].(string)
		if t == "object" {
			if _, hasProps := v["properties"]; !hasProps {
				if _, hasAP := v["additionalProperties"]; !hasAP {
					v["additionalProperties"] = true
					changed = true
				}
			}
		}
		for key, child := range v {
			// The boolean true we just set is not a schema; skip it. A map
			// or array child is a schema node or schema container.
			if b, ok := child.(bool); ok && key == "additionalProperties" && b {
				continue
			}
			if walkMoonshotSchema(child) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range v {
			if walkMoonshotSchema(child) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}
