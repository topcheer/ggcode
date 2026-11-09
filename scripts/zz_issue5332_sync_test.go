package main

import (
	"os"
	"strings"
	"testing"
)

// Pins #5332: the sync pipeline (scripts/sync-model-caps.go, a
// //go:build ignore generator) must sanitize known upstream mislabels
// before generation - a visionOverrides table applied at entry
// construction, so upstream models.dev flips of openai/o3-mini (text-only)
// to vision-capable get forced back before codegen and the #5320
// drift-guard pins keep passing ("fix the pipeline, not the pin").
// The generator is excluded from package compilation (go:build ignore),
// so per zz_issue5050 convention these assertions run on source text.
func TestIssue5332VisionOverrideSanitizeLayerPresent(t *testing.T) {
	src, err := os.ReadFile("sync-model-caps.go")
	if err != nil {
		t.Fatalf("read generator source: %v", err)
	}
	s := string(src)

	// The sanitize table must exist and cite why it overrides upstream.
	for _, want := range []string{
		"var visionOverrides = map[string]bool{",
		`"openai/o3-mini":      false,`,
		`"openai/o3-mini-high": false,`,
		"#5332 sanitize layer",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("generator missing sanitize-layer fragment %q", want)
		}
	}

	// The override must be applied at entry construction (before codegen):
	// a known-id lookup that forces SupportsVision.
	if !strings.Contains(s, "if keep, known := visionOverrides[id]; known {") ||
		!strings.Contains(s, "e.SupportsVision = keep") {
		t.Error("generator must apply visionOverrides at entry construction (SupportsVision forced)")
	}

	// The text-only correction must precede generation of both ids so the
	// drift-guard pins stay green across upstream re-introductions.
	for _, id := range []string{"o3-mini", "o3-mini-high"} {
		if !strings.Contains(s, `"openai/`+id+`"`) {
			t.Errorf("generator override table missing upstream-mislabel correction for %q", id)
		}
	}
}
