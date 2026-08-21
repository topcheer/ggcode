package tool

import (
	"strings"
	"testing"
)

// Guards from the #818-#838 fix rounds.

// #825: secret masking whitelist.
func TestConfigSetDisplayValue_Whitelist(t *testing.T) {
	if got := configSetDisplayValue("max_tokens", "8192"); got == "(secret stored securely)" {
		t.Errorf("max_tokens must display its value, got masked")
	}
	if got := configSetDisplayValue("context_tokens", "1000"); got == "(secret stored securely)" {
		t.Errorf("context_tokens must display its value, got masked")
	}
	for _, k := range []string{"api_key", "auth_token", "github_api_key", "PASSWORD"} {
		if got := configSetDisplayValue(k, "x"); got != "(secret stored securely)" {
			t.Errorf("%s must be masked, got %q", k, got)
		}
	}
}

// #837: legal git refs accepted, forbidden ones rejected.
func TestValidateRefName_GitRules(t *testing.T) {
	for _, ref := range []string{"release$v2", "fix(typo)", "quote\"name", "a;b"} {
		if err := validateRefName(ref); err != nil {
			t.Errorf("legal ref %q rejected: %v", ref, err)
		}
	}
	for _, ref := range []string{"..", "a~b", "a:b", "refs/heads/x.lock"} {
		if err := validateRefName(ref); err == nil {
			t.Errorf("forbidden ref %q accepted", ref)
		}
	}
}

// #830: multi-segment ** suffix must match (guard stays closed).
func TestMatchDoubleStar_MultiSegmentSuffix(t *testing.T) {
	ok, err := matchDoubleStar("src/**/gen/config.yaml", "src/a/gen/config.yaml")
	if err != nil || !ok {
		t.Errorf("multi-segment suffix failed: ok=%v err=%v", ok, err)
	}
}

// #829: recursive glob suffix semantics.
func TestMatchStarStarSuffix_Semantics(t *testing.T) {
	if !matchStarStarSuffix("*.go", "sub/nested.go") {
		t.Errorf("*.go must match base of sub/nested.go")
	}
	if !matchStarStarSuffix("gen/config.yaml", "a/b/gen/config.yaml") {
		t.Errorf("multi-segment suffix must match tail")
	}
}

// #835: sensitive-file patterns anchored to path boundaries.
func TestCheckSensitiveFiles_Anchored(t *testing.T) {
	if w := checkSensitiveFiles([]string{"app.environment.go"}); strings.Contains(w, "app.environment.go") {
		t.Errorf("app.environment.go must not be flagged as .env: %s", w)
	}
}
