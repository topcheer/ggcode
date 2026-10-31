package provider

import (
	"encoding/json"

	"github.com/topcheer/ggcode/internal/debug"
)

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

// normalizeMoonshotSchema returns params rewritten for moonshot-flavored
// validators, in three passes (#4276 round 2, after live evidence that
// pass 3 alone does not clear the "infinite recursion" rejection):
//
//  1. dereference: inline every "$ref" ("#/$defs/X" / "#/definitions/X"),
//     depth-capped and budget-capped; a reference that would recurse forever
//     (a cycle - exactly what walle's "detected infinite recursion without
//     termination condition" flags), exceeds the caps, or cannot be resolved
//     is replaced by the permissive fallback {"type":"object",
//     "additionalProperties":true} while keeping sibling keywords.
//  2. strip "$defs"/"definitions" - nothing references them any more, and
//     walle requires client-side dereferencing anyway.
//  3. every free-form object node ({"type":"object"} lacking both
//     "properties" and "additionalProperties") gains an explicit
//     "additionalProperties": true.
//
// It is idempotent and never narrows any schema: nodes that already declare
// properties or an additionalProperties policy (including false) are left
// untouched by pass 3, and pass 1 only removes indirection.
//
// Reference-graph semantics are modeled on MoonshotAI/walle (the open-source
// implementation behind the API-side validator): cycles of $ref through
// required hops - or array self-reference with minItems>=1 - have no finite
// instance and are rejected. Rather than reproducing walle's exact
// termination analysis, we cut EVERY cycle by construction (any $ref that
// needs more than moonshotMaxRefDepth hops is replaced), which is a
// superset defense: schemas walle would still accept are merely loosened
// (dropped constraints on the deepest cut), never tightened.
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
	// Pass 4 (#4276 round 3): size guard. MoonshotAI/walle caps each tool
	// schema at 120000 serialized bytes ("schema exceeds maximum allowed
	// size" at root). Pass-1 dereferencing can inflate a schema whose
	// $defs are referenced many times, and some MCP tool schemas are simply
	// huge to begin with. Either way the tool must stay usable: degrade the
	// parameters to the permissive free-form object (the tool-level
	// description, which carries the calling convention, lives outside the
	// parameters schema and is untouched). Runs regardless of `changed`:
	// an untouched-but-oversized schema must still degrade.
	if len(out) > moonshotMaxSchemaBytes {
		debug.Log("provider", "moonshot normalize: tool schema %d bytes exceeds %d cap, degrading to free-form object", len(out), moonshotMaxSchemaBytes)
		return json.RawMessage(`{"type":"object","additionalProperties":true}`)
	}
	if !changed {
		return params
	}
	return out
}

// moonshotMaxSchemaBytes is the per-tool-parameters size cap for moonshot
// endpoints. The walle default limit is 120000 serialized bytes; degrade
// below that to leave serialization headroom.
const moonshotMaxSchemaBytes = 100000

// moonshotMaxRefDepth caps how many $ref hops may be inlined before the
// fallback kicks in. Real-world schemas chain at most two or three defs;
// anything beyond that is either a cycle or an adversarial diamond, both of
// which must be cut for walle's termination check.
const moonshotMaxRefDepth = 4

// moonshotDerefNodeBudget caps the total number of nodes produced by pass 1
// across the whole schema, bounding exponential diamond blow-up.
const moonshotDerefNodeBudget = 8000

// moonshotRefFallback is the permissive terminal schema substituted for
// unresolvable, cyclic, or over-depth references.
var moonshotRefFallback = map[string]any{
	"type":                 "object",
	"additionalProperties": true,
}

// containsKeyMoonshot reports whether the subtree contains any object key
// equal to key. It is the cheap pre-pass that keeps ref-free schemas (all
// builtin tools) on a single scan before walkMoonshotSchema.
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

// collectMoonshotDefs gathers the root-level "$defs"/"definitions" tables
// the $refs may point at. Nested defs tables are not supported (references
// into them simply fail to resolve and hit the fallback).
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

// resolveMoonshotRef parses "#/$defs/NAME" and "#/definitions/NAME".
func resolveMoonshotRef(ref string) (string, bool) {
	for _, prefix := range []string{"#/$defs/", "#/definitions/"} {
		if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
			return ref[len(prefix):], true
		}
	}
	return "", false
}

// expandMoonshotRefs returns node with every $ref inlined. depth counts
// inlined hops; budget counts nodes produced. Cycles surface as depth
// exhaustion; diamonds as budget exhaustion; both terminate at the
// fallback. Siblings of a $ref survive the merge (the inlined body wins on
// key conflicts, since the referenced definition is the semantic base).
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
			// sibling keywords ride along on the inlined body.
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
			// defs tables are consumed by collectMoonshotDefs; they are
			// dropped at every level here (the resolver only reads the root
			// table, so nothing can reference a nested one).
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

// withFallbackSiblings replaces the "$ref" of a schema node with the
// permissive fallback, keeping every sibling keyword.
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

// deepCopyMoonshotValue clones parsed-JSON values (maps, slices, scalars).
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
