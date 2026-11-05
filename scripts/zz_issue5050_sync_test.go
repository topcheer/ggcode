package main

import (
	"os"
	"strings"
	"testing"
)

// #5050: the generator template in sync-model-caps.go previously embedded the
// pre-fix parseContextWindowHint (non-overlapping FindAllStringSubmatch), so
// regenerating internal/config/context_window.go would silently revert the
// #5052 boundary fix. Pin the template to the fixed scan shape (source-text
// assertions - the script is //go:build ignore, per the #2857 precedent).
func TestIssue5050GeneratorTemplateCarriesBoundaryFix(t *testing.T) {
	src, err := os.ReadFile("sync-model-caps.go")
	if err != nil {
		t.Fatalf("read generator: %v", err)
	}
	code := string(src)

	if !strings.Contains(code, "FindStringSubmatchIndex(s[i:])") {
		t.Errorf("generator template lost the #5050 boundary fix: manual FindStringSubmatchIndex scan missing")
	}
	if !strings.Contains(code, "i += loc[7]") {
		t.Errorf("generator template lost the #5050 advance-past-suffix step: 'i += loc[7]' missing")
	}
	if strings.Contains(code, "FindAllStringSubmatch(strings.ToLower(model), -1)") {
		t.Errorf("generator template still carries the pre-#5050 non-overlapping FindAllStringSubmatch scan (boundary double-consumption regression)")
	}
}
