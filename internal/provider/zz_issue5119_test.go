package provider

import "testing"

// TestIssue5119GLM5ContextWindows pins the glm-5.x entries added to
// knownModelContextWindows (#5119: sub-agents on glm-5.x models previously
// missed the table and fell back to the 128k default, compacting immediately).
//
// Values mirror the canonical vendor-scoped table in
// internal/config/context_window.go (base-name entries).
func TestIssue5119GLM5ContextWindows(t *testing.T) {
	cases := []struct {
		model  string
		window int
	}{
		{"glm-5.3", 1_048_576},
		{"glm-5.3-flash", 1_000_000},
		{"glm-5.3-flashx", 1_000_000},
		{"glm-5.3-highspeed", 1_000_000},
		{"glm-5.2", 1_048_576},
		{"glm-5.2-fast", 1_000_000},
		{"glm-5.1", 204_800},
		{"glm-5-turbo", 200_000},
		{"glm-5-free", 204_800},
		{"glm-5v-turbo", 200_000},
		{"glm-4.5-flash", 131_072},
		{"glm-4.5v", 64_000},
		{"glm-4.6v", 128_000},
		{"glm-4.7-flash", 200_000},
		{"glm-4.7-flashx", 200_000},
		{"glm-4.7-free", 204_800},
	}
	for _, tc := range cases {
		if got := LookupKnownModelContextWindow(tc.model); got != tc.window {
			t.Errorf("LookupKnownModelContextWindow(%q) = %d, want %d", tc.model, got, tc.window)
		}
	}
}

// TestIssue5119RetiredFamilyStillProbes reconciles #5119 with the #1287
// contract: bare glm-4.5, glm-4.5-air, glm-4.6, glm-4.7 and glm-5 keep NO
// static entry (must stay on the live-probe path) even though their window
// values are known - see TestIssue1287_GLM4ExactOnly.
func TestIssue5119RetiredFamilyStillProbes(t *testing.T) {
	for _, victim := range []string{"glm-4.5", "glm-4.5-air", "glm-4.6", "glm-4.7", "glm-5"} {
		if got := LookupKnownModelContextWindow(victim); got != 0 {
			t.Errorf("#1287 contract broken: %s got static window %d, want 0 (probe path)", victim, got)
		}
	}
}

// TestIssue5119UnknownModelFallbackUnchanged asserts an unknown model still
// returns 0 so initialContextWindow keeps its defaultContextWindow fallback.
func TestIssue5119UnknownModelFallbackUnchanged(t *testing.T) {
	for _, m := range []string{"", "totally-unknown-model", "zzz"} {
		if got := LookupKnownModelContextWindow(m); got != 0 {
			t.Errorf("LookupKnownModelContextWindow(%q) = %d, want 0 (fallback path)", m, got)
		}
	}
}

// TestIssue5119PrefixOrderGuards pins the order-sensitive prefix matching:
// a more specific variant must not be captured by its general prefix entry,
// and pre-existing glm-4 entries must keep their original behavior.
func TestIssue5119PrefixOrderGuards(t *testing.T) {
	if got := LookupKnownModelContextWindow("glm-5.3-flash"); got == 1_048_576 {
		t.Errorf("glm-5.3-flash incorrectly captured by glm-5.3 entry (got %d)", got)
	}
	if got := LookupKnownModelContextWindow("glm-5.2"); got == 204_800 {
		t.Errorf("glm-5.2 incorrectly captured by a glm-5 family entry (got %d)", got)
	}
	if got := LookupKnownModelContextWindow("GLM-5.3"); got != 1_048_576 {
		t.Errorf("case-insensitive lookup broke: got %d, want 1048576", got)
	}
	// Pre-existing glm-4 behavior must be unchanged.
	if got := LookupKnownModelContextWindow("glm-4-plus"); got != 128_000 {
		t.Errorf("glm-4-plus = %d, want 128000 (regression)", got)
	}
	if got := LookupKnownModelContextWindow("glm-4"); got != 128_000 {
		t.Errorf("glm-4 exact = %d, want 128000 (regression)", got)
	}
	if got := LookupKnownModelContextWindow("glm-4-long"); got != 1_000_000 {
		t.Errorf("glm-4-long = %d, want 1000000 (regression)", got)
	}
}
