package agent

// #4079: noArgTemplates keyed their speculative results by hash("{}")，but
// the schemas of git_status/git_branch_list/git_log mark description as
// REQUIRED, so every real invocation hashed {"description":"..."} and never
// matched the template — 0% hit by construction, plus each miss counted in
// the adaptive-threshold denominator and pushed the speculator to its most
// conservative setting. cacheKey now strips description for template tools.
import (
	"encoding/json"
	"testing"
)

func TestCacheKeyStripsDescriptionForTemplateTools(t *testing.T) {
	// The issue's core repro: a bare-but-schema-required real call must hit
	// the `{}` template's key.
	if cacheKey("git_status", json.RawMessage(`{"description":"check tree"}`)) !=
		cacheKey("git_status", json.RawMessage(`{}`)) {
		t.Errorf("bare git_status call with description does not key-match the noArg template")
	}
	if cacheKey("git_branch_list", json.RawMessage(`{"description":"list branches"}`)) !=
		cacheKey("git_branch_list", noArgTemplates["git_branch_list"]) {
		t.Errorf("bare git_branch_list call with description does not key-match the template")
	}
	// Semantic args still change the key: a path-scoped git_status must NOT
	// consume the bare template's result.
	if cacheKey("git_status", json.RawMessage(`{"description":"d","path":"."}`)) ==
		cacheKey("git_status", json.RawMessage(`{}`)) {
		t.Errorf("path-bearing git_status call collides with bare template key")
	}
	// Minimality: description is preserved for tools without a template —
	// the normalization must not leak into arg-linked predictions.
	if cacheKey("read_file", json.RawMessage(`{"path":"a","description":"d"}`)) ==
		cacheKey("read_file", json.RawMessage(`{"path":"a"}`)) {
		t.Errorf("description stripped for non-template tool read_file")
	}
	// Invalid JSON and non-objects pass through without panic.
	_ = cacheKey("git_log", json.RawMessage(`notjson`))
	_ = cacheKey("git_log", json.RawMessage(`null`))
	_ = cacheKey("git_log", json.RawMessage(`[]`))
}
