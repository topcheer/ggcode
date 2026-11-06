package config

import "testing"

// Issue #5123 companion pins for LookupSyncedContextWindow itself:
// exact vendor/base rows, @region suffix strip, path-segment fallback,
// and the 0-for-unknown contract callers rely on for fallthrough.
func TestIssue5123LookupSyncedContextWindow(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"glm-5.3", 1048576},                    // base row exact
		{"GLM-5.3 ", 1048576},                   // case/space normalization
		{"z-ai/glm-5.3-flashx", 1048576},        // vendor-scoped exact row
		{"glm-5.2@eu", 1048576},                 // @region suffix exact
		{"glm-5.3-flashx@eu", 1000000},          // @region suffix exact (flashx@eu row)
		{"@cf/zai-org/glm-5.3", 1048576},        // vendor prefix exact
		{"@cf/zai-org/glm-5.3-flashx", 1000000}, // no @cf flashx row -> segment fallback
		{"", 0},                                 // empty -> unknown
		{"no-such-model", 0},                    // unknown -> 0 for caller fallthrough
	}
	for _, tc := range cases {
		if got := LookupSyncedContextWindow(tc.model); got != tc.want {
			t.Errorf("LookupSyncedContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}
