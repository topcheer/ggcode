//go:build goolm

package wailskit

import (
	"os"
	"strings"
	"testing"
)

// #4499 pin: the impersonateCustomVersion consumer must sit OUTSIDE the
// impersonatePreset branch - a form submitting only the changed version
// (omitting the unchanged preset) used to have the update silently
// dropped.
func TestIssue4499_CustomVersionConsumedIndependentlyOfPreset(t *testing.T) {
	b, err := os.ReadFile("config.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	cvIdx := strings.Index(s, `if cv, ok := values["impersonateCustomVersion"].(string); ok {`)
	// Anchor on the CONSUMPTION branch (the one assigning
	// cfg.Impersonation.Preset), not the #3770 pre-validation lookup at
	// the top of the function.
	presetConsumeIdx := strings.Index(s, "cfg.Impersonation.Preset = v")
	if cvIdx < 0 || presetConsumeIdx < 0 {
		t.Fatal("config.go structure changed: both consumers expected")
	}
	if cvIdx > presetConsumeIdx {
		t.Fatal("#4499 regression: CustomVersion consumer is back inside the preset branch")
	}
}
