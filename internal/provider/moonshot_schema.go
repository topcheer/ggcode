package provider

import (
	"encoding/json"

	"github.com/topcheer/ggcode/internal/debug"
)

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

func normalizeMoonshotSchema(params json.RawMessage) json.RawMessage {
	if len(params) == 0 || !json.Valid(params) {
		return params
	}
	var doc any
	if err := json.Unmarshal(params, &doc); err != nil {
		return params
	}
	root, _ := doc.(map[string]any)
	derefBudget := moonshotDerefNodeBudget
	var changed bool
	if root != nil && containsKeyMoonshot(doc, "$ref") {
		defs := collectMoonshotDefs(root)
		doc = expandMoonshotRefs(doc, defs, 0, &derefBudget)
		root, _ = doc.(map[string]any)
		changed = true
	}
	if root != nil {
		if _, ok := root["$defs"]; ok {
			delete(root, "$defs")
			changed = true
		}
		if _, ok := root["definitions"]; ok {
			delete(root, "definitions")
			changed = true
		}
	}
	if walkMoonshotSchema(doc) {
		changed = true
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return params
	}

	if len(out) > moonshotMaxSchemaBytes {
		debug.Log("provider", "moonshot normalize: tool schema %d bytes exceeds %d cap, degrading to free-form object", len(out), moonshotMaxSchemaBytes)
		return json.RawMessage(`{"type":"object","additionalProperties":true}`)
	}
	if !changed {
		return params
	}
	return out
}

const moonshotMaxSchemaBytes = 100000

const moonshotMaxRefDepth = 4

const moonshotDerefNodeBudget = 8000

var moonshotRefFallback = map[string]any{
	"type":                 "object",
	"additionalProperties": true,
}

func containsKeyMoonshot(node any, key string) bool {
	switch v := node.(type) {
	case map[string]any:
		if _, ok := v[key]; ok {
			return true
		}
		for _, child := range v {
			if containsKeyMoonshot(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsKeyMoonshot(child, key) {
				return true
			}
		}
	}
	return false
}

func collectMoonshotDefs(root map[string]any) map[string]any {
	defs := make(map[string]any)
	for _, table := range []string{"$defs", "definitions"} {
		if m, ok := root[table].(map[string]any); ok {
			for k, v := range m {
				defs[k] = v
			}
		}
	}
	return defs
}

func resolveMoonshotRef(ref string) (string, bool) {
	for _, prefix := range []string{"#/$defs/", "#/definitions/"} {
		if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
			return ref[len(prefix):], true
		}
	}
	return "", false
}

func expandMoonshotRefs(node any, defs map[string]any, depth int, budget *int) any {
	*budget--
	if *budget < 0 {
		return cloneMoonshotFallback()
	}
	switch v := node.(type) {
	case map[string]any:
		ref, hasRef := v["$ref"].(string)
		if hasRef {
			if depth >= moonshotMaxRefDepth {
				return withFallbackSiblings(v)
			}
			name, ok := resolveMoonshotRef(ref)
			target, found := defs[name]
			if !ok || !found {
				return withFallbackSiblings(v)
			}
			expanded := expandMoonshotRefs(deepCopyMoonshotValue(target), defs, depth+1, budget)
			body, isMap := expanded.(map[string]any)
			if !isMap {
				return withFallbackSiblings(v)
			}

			for k, sv := range v {
				if k == "$ref" {
					continue
				}
				if _, exists := body[k]; !exists {
					body[k] = sv
				}
			}
			return body
		}
		out := make(map[string]any, len(v))
		for k, child := range v {

			if k == "$defs" || k == "definitions" {
				continue
			}
			out[k] = expandMoonshotRefs(child, defs, depth, budget)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = expandMoonshotRefs(child, defs, depth, budget)
		}
		return out
	default:
		return node
	}
}

func withFallbackSiblings(v map[string]any) map[string]any {
	out := make(map[string]any, len(v)+2)
	for k, sv := range v {
		if k == "$ref" {
			continue
		}
		out[k] = sv
	}
	for k, fv := range moonshotRefFallback {
		if _, exists := out[k]; !exists {
			out[k] = fv
		}
	}
	return out
}

func cloneMoonshotFallback() map[string]any {
	out := make(map[string]any, len(moonshotRefFallback))
	for k, v := range moonshotRefFallback {
		out[k] = v
	}
	return out
}

func deepCopyMoonshotValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			out[k] = deepCopyMoonshotValue(child)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = deepCopyMoonshotValue(child)
		}
		return out
	default:
		return v
	}
}

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
