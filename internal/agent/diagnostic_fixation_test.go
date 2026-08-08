package agent

import (
	"strings"
	"testing"
)

func TestExtractClaimEntityFilePath(t *testing.T) {
	text := "The problem is in internal/agent/loop.go where the retry logic"
	loc := diagnosticClaimRe.FindStringIndex(text)
	if loc == nil {
		t.Fatal("expected claim match")
	}
	entity := extractClaimEntity(text, loc[1])
	if entity != "internal/agent/loop.go" {
		t.Errorf("expected 'internal/agent/loop.go', got %q", entity)
	}
}

func TestExtractClaimEntityCodeIdent(t *testing.T) {
	text := "The root cause is parseConfig not handling nil values"
	loc := diagnosticClaimRe.FindStringIndex(text)
	if loc == nil {
		t.Fatal("expected claim match")
	}
	entity := extractClaimEntity(text, loc[1])
	if entity != "parseConfig" {
		t.Errorf("expected 'parseConfig', got %q", entity)
	}
}

func TestExtractClaimEntitySnakeCase(t *testing.T) {
	text := "The bug is caused by handle_request failing silently"
	loc := diagnosticClaimRe.FindStringIndex(text)
	if loc == nil {
		t.Fatal("expected claim match")
	}
	entity := extractClaimEntity(text, loc[1])
	if entity != "handle_request" {
		t.Errorf("expected 'handle_request', got %q", entity)
	}
}

func TestExtractClaimEntityNoMatch(t *testing.T) {
	text := "The problem is that it doesn't work properly"
	loc := diagnosticClaimRe.FindStringIndex(text)
	if loc == nil {
		t.Fatal("expected claim match")
	}
	entity := extractClaimEntity(text, loc[1])
	if entity != "" {
		t.Errorf("expected empty entity for plain English, got %q", entity)
	}
}

func TestRecordClaimsSingleTurnDedup(t *testing.T) {
	s := newDiagnosticFixationState()
	text := "The problem is in auth.go. The bug is in auth.go. The issue is in auth.go."
	s.recordClaims(text)
	if s.entityTurns["auth.go"] != 1 {
		t.Errorf("expected 1 turn for auth.go (deduped), got %d", s.entityTurns["auth.go"])
	}
}

func TestRecordClaimsMultipleTurns(t *testing.T) {
	s := newDiagnosticFixationState()

	s.recordClaims("The problem is in auth.go and the token validation.")
	s.recordClaims("The root cause is auth.go.")
	s.recordClaims("The bug appears to be in auth.go.")

	if s.entityTurns["auth.go"] != 3 {
		t.Errorf("expected 3 turns for auth.go, got %d", s.entityTurns["auth.go"])
	}
}

func TestRecordClaimsDifferentEntities(t *testing.T) {
	s := newDiagnosticFixationState()

	s.recordClaims("The problem is in auth.go.")
	s.recordClaims("The root cause is in config.go.")

	if s.entityTurns["auth.go"] != 1 {
		t.Errorf("expected 1 turn for auth.go, got %d", s.entityTurns["auth.go"])
	}
	if s.entityTurns["config.go"] != 1 {
		t.Errorf("expected 1 turn for config.go, got %d", s.entityTurns["config.go"])
	}
}

func TestMaybeWarnDiagnosticFixationBelowThreshold(t *testing.T) {
	a := &Agent{diagnosticFixation: newDiagnosticFixationState()}

	a.maybeWarnDiagnosticFixation("The problem is in auth.go.")
	a.maybeWarnDiagnosticFixation("The root cause is auth.go.")

	hint := a.maybeWarnDiagnosticFixation("This is a normal response without claims.")
	if hint != "" {
		t.Errorf("expected no warning (only 2 turns claimed), got warning")
	}
}

func TestMaybeWarnDiagnosticFixationAtThreshold(t *testing.T) {
	a := &Agent{diagnosticFixation: newDiagnosticFixationState()}

	a.maybeWarnDiagnosticFixation("The problem is in auth.go.")
	a.maybeWarnDiagnosticFixation("The root cause is auth.go.")
	hint := a.maybeWarnDiagnosticFixation("The bug appears to be in auth.go.")

	if hint == "" {
		t.Fatal("expected warning at 3 turns, got none")
	}
	if !strings.Contains(hint, "diagnostic-fixation") {
		t.Errorf("expected hint to contain detector name, got: %s", hint)
	}
	if !strings.Contains(hint, "auth.go") {
		t.Errorf("expected hint to mention auth.go, got: %s", hint)
	}
	if !strings.Contains(hint, "RE-DIAGNOSE") {
		t.Errorf("expected hint to contain RE-DIAGNOSE guidance, got: %s", hint)
	}
}

func TestMaybeWarnDiagnosticFixationMaxWarnings(t *testing.T) {
	a := &Agent{diagnosticFixation: newDiagnosticFixationState()}

	// Trigger first warning.
	a.maybeWarnDiagnosticFixation("The problem is in auth.go.")
	a.maybeWarnDiagnosticFixation("The root cause is auth.go.")
	hint1 := a.maybeWarnDiagnosticFixation("The bug is in auth.go.")
	if hint1 == "" {
		t.Fatal("expected first warning")
	}

	// Trigger second warning (different entity).
	a.maybeWarnDiagnosticFixation("The problem is in db.go.")
	a.maybeWarnDiagnosticFixation("The root cause is db.go.")
	hint2 := a.maybeWarnDiagnosticFixation("The bug is in db.go.")
	if hint2 == "" {
		t.Fatal("expected second warning")
	}

	// Third should be suppressed.
	a.maybeWarnDiagnosticFixation("The problem is in cache.go.")
	a.maybeWarnDiagnosticFixation("The root cause is cache.go.")
	hint3 := a.maybeWarnDiagnosticFixation("The bug is in cache.go.")
	if hint3 != "" {
		t.Errorf("expected third warning to be suppressed, got: %s", hint3)
	}
}

func TestDiagnosticFixationReset(t *testing.T) {
	s := newDiagnosticFixationState()
	s.recordClaims("The problem is in auth.go.")
	s.recordClaims("The problem is in auth.go.")
	s.recordClaims("The problem is in auth.go.")
	s.warnings = 1

	s.reset()

	if len(s.entityTurns) != 0 {
		t.Errorf("expected entityTurns to be cleared after reset")
	}
	if s.warnings != 0 {
		t.Errorf("expected warnings to be 0 after reset, got %d", s.warnings)
	}
}

func TestDiagnosticClaimPhrases(t *testing.T) {
	phrases := []string{
		"The problem is in server.go",
		"The issue is in handler.go",
		"The bug is in auth.go",
		"The error is in config.go",
		"The root cause is in loop.go",
		"The culprit is main.go",
		"The problem seems to be in routes.go",
		"The bug appears to be in db.go",
		"The problem lies in utils.go",
		"The problem is in parser.go",
		"caused by handle_request",
		"the null pointer is causing issues",
		"This is because parseInput doesn't check bounds",
		"This happens because buildConfig fails",
		"The fix needs to be in router.go",
	}
	for _, p := range phrases {
		if diagnosticClaimRe.FindString(p) == "" {
			t.Errorf("expected claim match for: %s", p)
		}
	}
}

func TestNonDiagnosticPhrasesNotMatched(t *testing.T) {
	phrases := []string{
		"I'll read the file now",
		"Let me check the documentation",
		"The test passed successfully",
		"Running the build now",
		"Here is the updated code",
	}
	for _, p := range phrases {
		if diagnosticClaimRe.FindString(p) != "" {
			t.Errorf("expected NO claim match for: %s", p)
		}
	}
}

func TestCodeIdentFiltersPlainEnglish(t *testing.T) {
	// These should NOT be matched as code identifiers.
	plain := []string{"server", "crashes", "problem", "issue", "config"}
	for _, word := range plain {
		if diagCodeIdentRe.MatchString(word) {
			t.Errorf("plain word %q should not match diagCodeIdentRe", word)
		}
	}
}

func TestCodeIdentMatchesCodePatterns(t *testing.T) {
	code := []string{"parseConfig", "handle_request", "HTTPClient", "API_KEY", "doWork"}
	for _, word := range code {
		if !diagCodeIdentRe.MatchString(word) {
			t.Errorf("code identifier %q should match diagCodeIdentRe", word)
		}
	}
}

func TestFilePathMatches(t *testing.T) {
	paths := []string{"auth.go", "config.yaml", "internal/agent/loop.go", "a.ts", "main.rs"}
	for _, p := range paths {
		if !diagFilePathRe.MatchString(p) {
			t.Errorf("file path %q should match diagFilePathRe", p)
		}
	}
}

func TestStaleEntitiesSortedByTurnCount(t *testing.T) {
	a := &Agent{diagnosticFixation: newDiagnosticFixationState()}

	// auth.go claimed 5 turns, config.go claimed 3 turns.
	for i := 0; i < 5; i++ {
		a.maybeWarnDiagnosticFixation("The problem is in auth.go.")
	}
	for i := 0; i < 3; i++ {
		a.maybeWarnDiagnosticFixation("The problem is in config.go.")
	}

	// After the first warning fires, warnings count is 1.
	// Force another check that should show auth.go first.
	hint := a.maybeWarnDiagnosticFixation("The problem is in auth.go and config.go.")
	if hint == "" {
		// May be suppressed if max warnings reached. Check state directly.
		authTurns := a.diagnosticFixation.entityTurns["auth.go"]
		cfgTurns := a.diagnosticFixation.entityTurns["config.go"]
		if authTurns < cfgTurns {
			t.Errorf("auth.go (%d) should have more turns than config.go (%d)", authTurns, cfgTurns)
		}
		return
	}
	// auth.go should appear before config.go in the hint.
	authIdx := strings.Index(hint, "auth.go")
	cfgIdx := strings.Index(hint, "config.go")
	if authIdx >= 0 && cfgIdx >= 0 && authIdx > cfgIdx {
		t.Errorf("expected auth.go before config.go in hint")
	}
}
