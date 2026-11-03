//go:build goolm

package wailskit

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4499_CustomVersionConsumedIndependentlyOfPreset(t *testing.T) {
	b, err := os.ReadFile("config.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	cvIdx := strings.Index(s, `if cv, ok := values["impersonateCustomVersion"].(string); ok {`)

	presetConsumeIdx := strings.Index(s, "cfg.Impersonation.Preset = v")
	if cvIdx < 0 || presetConsumeIdx < 0 {
		t.Fatal("config.go structure changed: both consumers expected")
	}
	if cvIdx > presetConsumeIdx {
		t.Fatal("#4499 regression: CustomVersion consumer is back inside the preset branch")
	}
}
