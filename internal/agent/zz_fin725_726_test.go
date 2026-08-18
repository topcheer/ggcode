package agent

// Regression tests for issue #725 (YAML duplicate-key detector: block scalar
// tracking + indentation stack) and issue #726 (HTTP plaintext IPv6).

import (
	"strings"
	"testing"
)

// --- #725: YAML duplicate keys ---

func TestYAMLDuplicateKeysBlockScalarNoFalsePositive(t *testing.T) {
	// GitHub Actions-style workflow: `run: |` block scalar whose shell content
	// contains repeated "Phase 1:"-like lines. Previously misreported as a
	// duplicate key, and the early return also suppressed real YAML validation.
	yaml := `name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: Deploy
        run: |
          echo "Phase 1: build"
          echo "Phase 2: test"
          echo "Phase 1: build again"
`
	if dups := findYAMLDuplicateKeys(yaml); len(dups) > 0 {
		t.Errorf("block scalar body misparsed as duplicate keys: %v", dups)
	}
	if w := validateYAML("ci.yml", yaml); w != "" {
		t.Errorf("valid workflow flagged: %q", w)
	}
}

func TestYAMLDuplicateKeysFoldedAndModifiers(t *testing.T) {
	variants := map[string]string{
		"literal":        "run: |\n  echo hi\n",
		"folded":         "run: >\n  echo hi\n",
		"strip":          "run: |-\n  echo hi\n",
		"keep":           "run: |+\n  echo hi\n\n",
		"explicit-ind":   "run: |2\n    echo hi\n",
		"ind-then-chomp": "run: >2-\n    echo hi\n",
	}
	for name, y := range variants {
		if dups := findYAMLDuplicateKeys("a: 1\n" + y); len(dups) > 0 {
			t.Errorf("%s: block scalar body misparsed: %v", name, dups)
		}
	}
}

func TestYAMLDuplicateKeysOneSpaceIndent(t *testing.T) {
	// 1-space-indented legal YAML: previously `indent/2` mapped the nested keys
	// to depth 0 and collided with top-level keys.
	yaml := `top:
 a: 1
 b: 2
a: 3
`
	// `a` appears at two different levels - legal. The old `indent/2` logic
	// mapped the 1-space-indented keys to depth 0 and falsely collided with
	// the top-level `a`.
	if dups := findYAMLDuplicateKeys(yaml); len(dups) > 0 {
		t.Errorf("1-space indent misreported as duplicate keys: %v", dups)
	}
	if w := validateYAML("x.yml", yaml); w != "" {
		t.Errorf("valid 1-space-indent YAML flagged: %q", w)
	}
}

func TestYAMLDuplicateKeysRealDuplicatesStillWarn(t *testing.T) {
	yaml := `name: first
on: push
name: second
`
	dups := findYAMLDuplicateKeys(yaml)
	if len(dups) == 0 || dups[0] != "name" {
		t.Errorf("real duplicate key not detected: %v", dups)
	}
	w := validateYAML("x.yml", yaml)
	if !strings.Contains(w, "duplicate key") {
		t.Errorf("validateYAML must warn on real duplicates, got: %q", w)
	}
	// Nested duplicate still detected (same level, not cross-level).
	nested := `a:
  b: 1
  b: 2
`
	if dups := findYAMLDuplicateKeys(nested); len(dups) == 0 || dups[0] != "b" {
		t.Errorf("nested duplicate key not detected: %v", dups)
	}
	// Same key at DIFFERENT levels is legal and must not warn.
	levels := `a:
  b: 1
c:
  b: 2
`
	if dups := findYAMLDuplicateKeys(levels); len(dups) > 0 {
		t.Errorf("same key at different levels misreported: %v", dups)
	}
}

// --- #726: HTTP plaintext IPv6 ---

func TestHTTPPlaintextIPv6(t *testing.T) {
	// Public IPv6 plaintext HTTP must warn (previously never matched).
	w := checkHTTPPlaintext("main.go", "", `u := "http://[2001:db8::1]/api"`)
	if len(w) == 0 {
		t.Error("public IPv6 plaintext HTTP not flagged")
	}
	// Loopback IPv6 must be exempt ([::1] whitelist entry now live).
	if w := checkHTTPPlaintext("main.go", "", `u := "http://[::1]:8080/x"`); len(w) != 0 {
		t.Errorf("[::1] not exempted: %v", w)
	}
}

func TestHTTPPlaintextIPv4AndDomainUnchanged(t *testing.T) {
	if w := checkHTTPPlaintext("main.go", "", `u := "http://93.184.216.34/api"`); len(w) == 0 {
		t.Error("public IPv4 plaintext HTTP not flagged")
	}
	if w := checkHTTPPlaintext("main.go", "", `u := "http://api.example.com/v1"`); len(w) == 0 {
		t.Error("domain plaintext HTTP not flagged")
	}
	if w := checkHTTPPlaintext("main.go", "", `u := "http://localhost:3000/dev"`); len(w) != 0 {
		t.Error("localhost not exempted")
	}
	if w := checkHTTPPlaintext("main.go", "", `u := "http://127.0.0.1:8080/x"`); len(w) != 0 {
		t.Error("127.0.0.1 not exempted")
	}
}
