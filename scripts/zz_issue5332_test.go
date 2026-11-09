package main

import (
	"os"
	"strings"
	"testing"
)

// #5332: local correction layer for models.dev known-bad rows.
//
// The weekly sync (run 42602486566) failed the drift-guard because upstream
// re-tagged openai/o3-mini and o3-mini-high as SupportsVision=true while the
// fact pin (internal/config context_window_test.go, from calibration batch
// #5320) correctly asserts text-only. The fix is an override table applied
// in collectProviders AFTER the upstream read and BEFORE code generation -
// never weakening the pin.
//
// The generator is //go:build ignore (not part of the package build), so
// per the #2857/#5050 precedents these tests pin the SOURCE TEXT of the
// generator: table present, applied at the collection point, fact pin
// untouched. Behavioral proof is the post-merge workflow rerun (#5332
// acceptance step 4).

func read5332(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// The override table exists with both known-bad IDs pinned to
// SupportsVision=false, and carries the provenance comment.
func TestIssue5332_OverrideTablePresent(t *testing.T) {
	src := read5332(t, "sync-model-caps.go")
	for _, needle := range []string{
		`"openai/o3-mini":      false`,
		`"openai/o3-mini-high": false`,
		"upstreamVisionOverrides",
		"#5320",
	} {
		if !strings.Contains(src, needle) {
			t.Errorf("override table missing %q in sync-model-caps.go", needle)
		}
	}
}

// The correction is applied inside collectProviders, after the upstream
// SupportsVision assignment, before the entry is appended - i.e. on the
// data path between fetch and code generation.
func TestIssue5332_OverrideAppliedOnCollectPath(t *testing.T) {
	src := read5332(t, "sync-model-caps.go")
	assign := strings.Index(src, "SupportsVision:  m.SupportsAttachments")
	apply := strings.Index(src, "if fixed, ok := upstreamVisionOverrides[e.SourceProvider+\"/\"+e.ID]; ok {")
	if assign < 0 || apply < 0 {
		t.Fatal("collect-path assignment or override application not found")
	}
	if apply <= assign {
		t.Errorf("override application (idx %d) must come after the upstream assignment (idx %d)", apply, assign)
	}
	// inside collectProviders, not some other function
	fn := strings.LastIndex(src[:assign], "func collectProviders")
	if fn < 0 || fn > assign {
		t.Error("override application is not inside collectProviders")
	}
}

// The drift-guard fact pin is NOT touched by this fix: the o3-mini
// want=false assertion in internal/config stays exactly as #5320 wrote it.
// (Changing the pin to satisfy upstream is the fix-the-test anti-pattern
// this issue exists to prevent.)
func TestIssue5332_FactPinUntouched(t *testing.T) {
	pin := read5332(t, "../internal/config/context_window_test.go")
	for _, needle := range []string{
		// #5320 fact pin: the literal table row asserting o3-mini text-only
		`{"openai/o3-mini", "openai", false},`,
		// and the drift-guard test itself still exists by name
		"func TestInferVisionSupportCapabilityTableOnly",
	} {
		if !strings.Contains(pin, needle) {
			t.Errorf("drift-guard fact pin damaged: %q missing from context_window_test.go", needle)
		}
	}
}
