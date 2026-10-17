package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// #4276 round 2: live evidence (2026-10-17, binary 727cc6ddf which already
// contained the round-1 additionalProperties fix) shows moonshot still
// rejecting with "At path 'properties.steps': detected infinite recursion
// without termination condition". MoonshotAI/walle - the open-source
// validator behind the API - defines that rejection as a $ref reference
// cycle (required-hop cycles, mutual refs, array self-reference with
// minItems>=1). Round 1 never touched $refs; round 2 dereferences them with
// cycle cutting. These cases replay walle's own invalid.jsonl corpus.

func mustNormalize4276R2(t *testing.T, in string) map[string]any {
	t.Helper()
	out := normalizeMoonshotSchema(json.RawMessage(in))
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("normalized schema not valid JSON: %v\noutput: %s", err, out)
	}
	return doc
}

func assertNoRefs4276R2(t *testing.T, doc map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), "$ref") {
		t.Fatalf("normalized schema still contains $ref: %s", raw)
	}
	if strings.Contains(string(raw), "$defs") {
		t.Fatalf("normalized schema still contains $defs: %s", raw)
	}
}

// walle invalid.jsonl: $defs self-reference through a required hop.
func Test4276R2_DefsSelfReferenceCycleCut(t *testing.T) {
	in := `{"$defs":{"XXX":{"properties":{"next":{"$ref":"#/$defs/XXX"}},"required":["next"],"type":"object"}},"properties":{"node":{"$ref":"#/$defs/XXX"}},"type":"object"}`
	doc := mustNormalize4276R2(t, in)
	assertNoRefs4276R2(t, doc)
	// The cycle must unwind into a FINITE chain: each hop retains the real
	// object shape, and the chain terminates (depth cap) in a permissive
	// free-form node with no further "next" - no infinite recursion left.
	node := doc["properties"].(map[string]any)["node"].(map[string]any)
	cur := node
	for hops := 0; hops < 10; hops++ {
		props, _ := cur["properties"].(map[string]any)
		next, _ := props["next"].(map[string]any)
		if next == nil {
			break
		}
		cur = next
		if hops == 9 {
			t.Fatalf("chain did not terminate within 10 hops: %v", node)
		}
	}
	if cur["type"] != "object" || cur["additionalProperties"] != true {
		t.Fatalf("chain must terminate in permissive object, got: %v", cur)
	}
	if props, _ := cur["properties"].(map[string]any); len(props) != 0 {
		t.Fatalf("terminal node must be free-form (no properties), got: %v", cur)
	}
	// Idempotent.
	once, _ := json.Marshal(doc)
	twice := normalizeMoonshotSchema(once)
	if string(once) != string(twice) {
		t.Fatalf("not idempotent:\nonce:  %s\ntwice: %s", once, twice)
	}
}

// walle invalid.jsonl: mutual references A <-> B through required hops.
func Test4276R2_MutualRefCycleCut(t *testing.T) {
	in := `{"type":"object","properties":{"root":{"$ref":"#/$defs/A"}},"required":["root"],"$defs":{"A":{"type":"object","properties":{"b":{"$ref":"#/$defs/B"}},"required":["b"]},"B":{"type":"object","properties":{"a":{"$ref":"#/$defs/A"}},"required":["a"]}}}`
	doc := mustNormalize4276R2(t, in)
	assertNoRefs4276R2(t, doc)
}

// walle invalid.jsonl: array self-reference with minItems>=1.
func Test4276R2_ArraySelfReferenceCycleCut(t *testing.T) {
	in := `{"type":"array","items":{"type":"object","properties":{"elements":{"$ref":"#"}},"required":["elements"]},"minItems":1}`
	doc := mustNormalize4276R2(t, in)
	assertNoRefs4276R2(t, doc)
}

// Unresolvable / non-local refs (walle: "references must start with #/$defs/")
// must fall back to the permissive object instead of failing the request.
func Test4276R2_UnresolvableRefFallsBack(t *testing.T) {
	in := `{"type":"object","properties":{"a":{"type":"string"},"b":{"$ref":"#/properties/a/type"}}}`
	doc := mustNormalize4276R2(t, in)
	assertNoRefs4276R2(t, doc)
	b := doc["properties"].(map[string]any)["b"].(map[string]any)
	if b["type"] != "object" || b["additionalProperties"] != true {
		t.Fatalf("unresolvable ref must cut to permissive object, got: %v", b)
	}
	// Sibling keywords of the ref survive.
	in2 := `{"type":"object","properties":{"x":{"$ref":"https://example.com/x.json","description":"keep me"}}}`
	doc2 := mustNormalize4276R2(t, in2)
	x := doc2["properties"].(map[string]any)["x"].(map[string]any)
	if x["description"] != "keep me" {
		t.Fatalf("ref sibling keywords must survive, got: %v", x)
	}
}

// A legitimate non-cyclic $ref must be inlined semantically (the referenced
// body appears at the reference site), not just cut.
func Test4276R2_LegitRefInlined(t *testing.T) {
	in := `{"type":"object","properties":{"sym":{"$ref":"#/$defs/Sym"}},"required":["sym"],"$defs":{"Sym":{"type":"string","minLength":1}}}`
	doc := mustNormalize4276R2(t, in)
	assertNoRefs4276R2(t, doc)
	sym := doc["properties"].(map[string]any)["sym"].(map[string]any)
	if sym["type"] != "string" || sym["minLength"].(float64) != 1 {
		t.Fatalf("legit ref body must be inlined, got: %v", sym)
	}
}

// Depth beyond moonshotMaxRefDepth is cut even without a cycle (diamonds and
// adversarial chains must not blow up the payload).
func Test4276R2_DeepChainCut(t *testing.T) {
	in := `{"type":"object","properties":{"p":{"$ref":"#/$defs/L1"}},"$defs":{"L1":{"properties":{"n":{"$ref":"#/$defs/L2"}}},"L2":{"properties":{"n":{"$ref":"#/$defs/L3"}}},"L3":{"properties":{"n":{"$ref":"#/$defs/L4"}}},"L4":{"properties":{"n":{"$ref":"#/$defs/L5"}}},"L5":{"type":"object"}}}`
	doc := mustNormalize4276R2(t, in)
	assertNoRefs4276R2(t, doc)
	// 5-hop chain: L1->L2->L3->L4 cut at depth cap, the tail is permissive.
	raw, _ := json.Marshal(doc)
	if !strings.Contains(string(raw), "additionalProperties") {
		t.Fatalf("depth-cut node must carry the permissive fallback: %s", raw)
	}
}

// Ref-free schemas (every builtin tool) take the cheap path and stay
// byte-identical apart from the round-1 free-form pass.
func Test4276R2_RefFreeUnchanged(t *testing.T) {
	in := `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`
	out := string(normalizeMoonshotSchema(json.RawMessage(in)))
	if out != in {
		t.Fatalf("ref-free closed schema must be unchanged, got: %s", out)
	}
}

// #4276 round 3: walle caps each tool schema at 120000 serialized bytes and
// answers 400 "schema exceeds maximum allowed size" at root. Dereferencing
// (pass 1) can inflate a schema with many refs to one big def; huge MCP tool
// schemas can be over the cap on arrival. Both must degrade to the
// permissive free-form object instead of failing every request in the
// session.
func Test4276R3_SizeGuardDegradesOversizedSchema(t *testing.T) {
	// A natively huge schema (the real-world case: large MCP tool schemas
	// arrive over the cap already, no deref inflation needed).
	props := make([]string, 260)
	sub := make([]string, 8)
	for j := range sub {
		sub[j] = fmt.Sprintf(`"s%d":{"type":"string","description":"this is padding to push the serialized schema size well beyond the wire cap for moonshot validators"}`, j)
	}
	for i := range props {
		props[i] = fmt.Sprintf(`"p%03d":{"type":"object","properties":{%s}}`, i, strings.Join(sub, ","))
	}
	in := `{"type":"object","properties":{` + strings.Join(props, ",") + `}}`
	if !json.Valid([]byte(in)) {
		t.Fatalf("test fixture must be valid JSON")
	}
	if len(in) <= moonshotMaxSchemaBytes {
		t.Fatalf("fixture must exceed the cap, got %d bytes", len(in))
	}
	out := normalizeMoonshotSchema(json.RawMessage(in))
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("degraded schema not valid JSON: %v", err)
	}
	if doc["type"] != "object" || doc["additionalProperties"] != true {
		t.Fatalf("oversized schema must degrade to free-form object, got: %s", out)
	}
	if _, has := doc["properties"]; has {
		t.Fatal("degraded schema must not carry properties")
	}
	if raw, _ := json.Marshal(doc); len(raw) > 100 {
		t.Fatalf("degraded schema must be tiny, got %d bytes", len(raw))
	}
}

// A schema just under the cap keeps its full shape (guard must not fire early).
func Test4276R3_SizeGuardKeepsUnderCap(t *testing.T) {
	props := make([]string, 150) // ~60KB, safely under 100000
	for i := range props {
		props[i] = fmt.Sprintf(`"p%03d":{"$ref":"#/$defs/S"}`, i)
	}
	in := `{"type":"object","properties":{` + strings.Join(props, ",") + `},"$defs":{"S":{"type":"object","properties":{"x":{"type":"string"},"y":{"type":"string"},"z":{"type":"string"}}}}}`
	out := normalizeMoonshotSchema(json.RawMessage(in))
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("normalized schema not valid JSON: %v", err)
	}
	if _, has := doc["properties"]; !has {
		t.Fatalf("under-cap schema must keep properties, got: %s", out)
	}
	if len(out) > moonshotMaxSchemaBytes {
		t.Fatalf("under-cap schema must stay under the cap, got %d bytes", len(out))
	}
}
