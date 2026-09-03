package agent

import (
	"testing"
)

func TestNewContradictionState(t *testing.T) {
	s := newContradictionState()
	if s == nil {
		t.Fatal("expected non-nil state")
	}
	if len(s.claims) != 0 || len(s.contradictions) != 0 {
		t.Fatal("expected empty initial state")
	}
}

func TestContradictionReset(t *testing.T) {
	s := newContradictionState()
	s.claims = append(s.claims, contradictionClaim{entity: "auth.go"})
	s.contradictions = append(s.contradictions, contradictionInstance{})
	s.warnings = 1
	s.reset()
	if len(s.claims) != 0 || len(s.contradictions) != 0 || s.warnings != 0 {
		t.Fatal("reset should clear all fields")
	}
}

func TestNormalizeContradictionEntity(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"auth.go", "auth.go"},
		{"Auth.go", "auth.go"},
		{"the", ""},
		{"it", ""},
		{"ab", ""},
		{"path/to/auth.go", "auth.go"},
		{"auth.go.", "auth.go"},
		{"auth.go,", "auth.go"},
		{"config", "config"},
	}
	for _, c := range cases {
		got := normalizeContradictionEntity(c.input)
		if got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestExtractClaimsBugIn(t *testing.T) {
	text := "I found the bug is in auth.go, specifically in the token validation."
	claims := extractClaims(text, 0)
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(claims))
	}
	if claims[0].entity != "auth.go" {
		t.Errorf("expected entity 'auth.go', got %q", claims[0].entity)
	}
}

func TestExtractClaimsRootCause(t *testing.T) {
	text := "After investigation, the root cause is the config parser."
	claims := extractClaims(text, 0)
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(claims))
	}
	if claims[0].entity != "config parser" {
		t.Errorf("expected entity 'config parser', got %q", claims[0].entity)
	}
}

func TestExtractClaimsCausing(t *testing.T) {
	text := "The retry loop is causing the timeout error."
	claims := extractClaims(text, 0)
	if len(claims) < 1 {
		t.Fatalf("expected at least 1 claim, got %d", len(claims))
	}
	// Should find "retry loop" as causing the timeout
	found := false
	for _, c := range claims {
		if c.entity == "retry loop" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find 'retry loop' entity")
	}
}

func TestExtractClaimsDedup(t *testing.T) {
	text := "The bug is in auth.go. The bug is in auth.go again."
	claims := extractClaims(text, 0)
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim after dedup, got %d", len(claims))
	}
}

func TestExtractClaimsEmptyShort(t *testing.T) {
	claims := extractClaims("short", 0)
	if claims != nil {
		t.Fatal("expected nil for short text")
	}
}

func TestContradictionDetection(t *testing.T) {
	s := newContradictionState()

	// First claim
	text1 := "The bug is in auth.go."
	s.recordContradictionClaims(text1, 0)
	if len(s.contradictions) != 0 {
		t.Fatalf("expected 0 contradictions after first claim, got %d", len(s.contradictions))
	}

	// Contradicting claim
	text2 := "The real issue is in router.go."
	s.recordContradictionClaims(text2, 1)
	if len(s.contradictions) != 1 {
		t.Fatalf("expected 1 contradiction, got %d", len(s.contradictions))
	}
}

func TestNoContradictionSameEntity(t *testing.T) {
	s := newContradictionState()

	s.recordContradictionClaims("The bug is in auth.go.", 0)
	s.recordContradictionClaims("The bug is in auth.go.", 1)
	if len(s.contradictions) != 0 {
		t.Fatalf("expected 0 contradictions for same entity, got %d", len(s.contradictions))
	}
}

func TestNoContradictionSubstring(t *testing.T) {
	s := newContradictionState()

	// "auth" and "auth.go" should not be treated as contradictions
	s.recordContradictionClaims("The bug is in auth.", 0)
	s.recordContradictionClaims("The bug is in auth.go.", 1)
	if len(s.contradictions) != 0 {
		t.Fatalf("expected 0 contradictions for substring entities, got %d", len(s.contradictions))
	}
}

func TestMultipleContradictions(t *testing.T) {
	s := newContradictionState()
	s.recordContradictionClaims("The bug is in auth.go.", 0)
	s.recordContradictionClaims("The issue is in router.go.", 1)
	s.recordContradictionClaims("The root cause is config.", 2)

	if len(s.contradictions) < 2 {
		t.Fatalf("expected >=2 contradictions, got %d", len(s.contradictions))
	}
}

func TestClaimBoundedMemory(t *testing.T) {
	s := newContradictionState()
	// Record more than contradictionMaxClaims
	for i := 0; i < contradictionMaxClaims+10; i++ {
		text := "The bug is in file" + string(rune('a'+i%26)) + ".go."
		s.recordContradictionClaims(text, i)
	}
	if len(s.claims) > contradictionMaxClaims {
		t.Errorf("claims not bounded: got %d, max %d", len(s.claims), contradictionMaxClaims)
	}
}

func TestMaybeWarnContradictionNoWarning(t *testing.T) {
	a := &Agent{contradiction: newContradictionState()}

	// Only 1 contradiction -- below threshold
	a.maybeWarnContradiction("The bug is in auth.go.", 0)
	result := a.maybeWarnContradiction("The issue is in router.go.", 1)
	if result != "" {
		t.Fatalf("expected no warning with <threshold contradictions, got: %s", result)
	}
}

func TestMaybeWarnContradictionWarning(t *testing.T) {
	a := &Agent{contradiction: newContradictionState()}

	a.maybeWarnContradiction("The bug is in auth.go.", 0)
	a.maybeWarnContradiction("The issue is in router.go.", 1)
	result := a.maybeWarnContradiction("The root cause is config.", 2)
	if result == "" {
		t.Fatal("expected warning with >=threshold contradictions")
	}
	if !contains(result, "[CONTRADICTION-DETECTED]") {
		t.Errorf("warning should contain tag, got: %s", result)
	}
}

func TestMaybeWarnContradictionMaxWarnings(t *testing.T) {
	a := &Agent{contradiction: newContradictionState()}

	// Trigger warnings
	for i := 0; i < 5; i++ {
		a.maybeWarnContradiction("The bug is in file.go.", i)
		a.maybeWarnContradiction("The issue is in other.go.", i)
	}
	if a.contradiction.warnings > contradictionMaxWarnings {
		t.Errorf("warnings should be capped at %d, got %d", contradictionMaxWarnings, a.contradiction.warnings)
	}
}

func TestMaybeWarnContradictionNil(t *testing.T) {
	a := &Agent{contradiction: nil}
	result := a.maybeWarnContradiction("The bug is in auth.go.", 0)
	if result != "" {
		t.Fatal("expected empty string when state is nil")
	}
}

// TestContradictionAcknowledgedRevisionNotCounted pins #1447-A: an
// explicitly acknowledged revision ("I was wrong - the root cause is B")
// is not a contradiction - the detector's own charter says 'without
// acknowledging'.
func TestContradictionAcknowledgedRevisionNotCounted(t *testing.T) {
	s := newContradictionState()
	// First claim in pattern-extractable shape: "X is the root cause".
	s.recordContradictionClaims("the auth module is the root cause of the crash.", 1)
	// Acknowledged revision: root cause is the retry loop.
	s.recordContradictionClaims("Earlier I thought auth.go, but I was wrong - the real issue is in the retry loop.", 2)
	if len(s.contradictions) != 0 {
		t.Fatalf("acknowledged revision counted as contradiction: %d", len(s.contradictions))
	}
	// WITHOUT acknowledgment the same pair still counts (guard stays live).
	s2 := newContradictionState()
	s2.recordContradictionClaims("the auth module is the root cause of the crash.", 1)
	s2.recordContradictionClaims("the retry loop is the root cause of the crash.", 2)
	if len(s2.contradictions) == 0 {
		t.Fatal("unacknowledged revision not counted - guard over-broad")
	}
}
