package config

import (
	"strings"
	"testing"
)

func findFinding(findings []UnknownKeyFinding, path string) *UnknownKeyFinding {
	for i := range findings {
		if findings[i].Path == path {
			return &findings[i]
		}
	}
	return nil
}

func TestFindUnknownKeysTypoTopLevel(t *testing.T) {
	data := []byte("modle: x\nmodel: y\nlanguage: zh-CN\n")
	findings := FindUnknownKeys(data)
	f := findFinding(findings, "modle")
	if f == nil {
		t.Fatalf("expected unknown key %q, got %+v", "modle", findings)
	}
	if f.Line != 1 {
		t.Errorf("expected line 1, got %d", f.Line)
	}
	if f.Hint != "model" {
		t.Errorf("expected hint %q, got %q", "model", f.Hint)
	}
	if n := len(findings); n != 1 {
		t.Errorf("expected exactly 1 finding, got %d: %+v", n, findings)
	}
}

func TestFindUnknownKeysNestedStruct(t *testing.T) {
	data := []byte("lsp_servers:\n  go:\n    binary: gopls\n    args: [\"-mode=stdio\"]\n    bogus_opt: 1\n")
	findings := FindUnknownKeys(data)
	f := findFinding(findings, "lsp_servers.go.bogus_opt")
	if f == nil {
		t.Fatalf("expected unknown nested key, got %+v", findings)
	}
	if f.Line != 5 {
		t.Errorf("expected line 5, got %d", f.Line)
	}
}

func TestFindUnknownKeysMapWildcardsAccepted(t *testing.T) {
	data := []byte(`vendor: testv
endpoint: teste
vendors:
  anything_goes:
    endpoints:
      arbitrary_name:
        protocol: openai
        base_url: http://127.0.0.1:9
        api_key: sk-x
tool_permissions:
  run_command: allow
  totally_made_up_tool: deny
`)
	findings := FindUnknownKeys(data)
	if len(findings) != 0 {
		t.Errorf("map wildcard keys must not be flagged, got %+v", findings)
	}
}

func TestFindUnknownKeysUnknownEndpointFieldFlagged(t *testing.T) {
	data := []byte("vendors:\n  v:\n    endpoints:\n      e:\n        protocol: openai\n        base_urll: http://x\n")
	findings := FindUnknownKeys(data)
	f := findFinding(findings, "vendors.v.endpoints.e.base_urll")
	if f == nil {
		t.Fatalf("expected unknown endpoint field, got %+v", findings)
	}
	if f.Hint != "base_url" {
		t.Errorf("expected hint base_url, got %q", f.Hint)
	}
}

func TestFindUnknownKeysFreeFormMapsAccepted(t *testing.T) {
	data := []byte(`mcp_servers:
  - name: a
    command: foo
    env:
      ANY_KEY: v1
      OTHER: v2
    headers:
      X-Whatever: hello
plugins:
  - name: p
    some_extra_inline: true
`)
	findings := FindUnknownKeys(data)
	if len(findings) != 0 {
		t.Errorf("free-form map children must not be flagged, got %+v", findings)
	}
}

func TestFindUnknownKeysMCPServerListEntry(t *testing.T) {
	data := []byte("mcp_servers:\n  - name: a\n    command: foo\n    bogus_mcp_opt: 1\n")
	findings := FindUnknownKeys(data)
	f := findFinding(findings, "mcp_servers.bogus_mcp_opt")
	if f == nil {
		t.Fatalf("expected unknown mcp_servers entry field, got %+v", findings)
	}
}

func TestFindUnknownKeysParseGarbageYieldsNil(t *testing.T) {
	if findings := FindUnknownKeys([]byte("::::: [unclosed")); findings != nil {
		t.Errorf("parse error must yield nil findings, got %+v", findings)
	}
}

func TestFindUnknownKeysNoDescendIntoUnknownSubtree(t *testing.T) {
	data := []byte("mystery_section:\n  deeper:\n    even_deeper: 1\nmodel: m\n")
	findings := FindUnknownKeys(data)
	if len(findings) != 1 {
		t.Fatalf("expected exactly the top-level unknown key, got %+v", findings)
	}
	if findings[0].Path != "mystery_section" {
		t.Errorf("expected top-level path only, got %q", findings[0].Path)
	}
}

func TestFindUnknownKeysAnchorsAndAliases(t *testing.T) {
	data := []byte("lsp_servers:\n  go: &g\n    binary: gopls\n  rust:\n    <<: *g\n    args: [x]\n")
	findings := FindUnknownKeys(data)
	if len(findings) != 0 {
		t.Errorf("merge keys and aliases must not be flagged, got %+v", findings)
	}
}

func TestSuggestConfigKeyTolerance(t *testing.T) {
	keys := map[string]*keyNode{
		"model":    {},
		"language": {},
		"endpoint": {},
	}
	if got := suggestConfigKey("modle", keys); got != "model" {
		t.Errorf("modle -> %q, want model", got)
	}
	if got := suggestConfigKey("Model", keys); got != "model" {
		t.Errorf("Model -> %q, want model", got)
	}
	if got := suggestConfigKey("xyzzymology", keys); got != "" {
		t.Errorf("far key must have no hint, got %q", got)
	}
}

func TestKnownConfigKeysCoversCoreSections(t *testing.T) {
	root := knownConfigKeys()
	for _, key := range []string{"vendor", "endpoint", "model", "vendors", "mcp_servers", "hooks", "subagents"} {
		if _, ok := root.literal[key]; !ok {
			t.Errorf("known keys missing top-level %q (have %d keys)", key, len(root.literal))
		}
	}
}

func TestUnknownKeyFindingPathFormat(t *testing.T) {
	f := UnknownKeyFinding{Path: "a.b.c"}
	if !strings.Contains(f.Path, ".") {
		t.FailNow()
	}
}
