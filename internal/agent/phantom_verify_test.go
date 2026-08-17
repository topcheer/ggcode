package agent

import (
	"strings"
	"testing"
)

func TestPhantomVerifyState_recordToolCall(t *testing.T) {
	s := newPhantomVerifyState()

	// Running a test command should mark test category as run
	s.recordToolCall("run_command", "go test ./internal/agent/", false)
	if !s.categoriesRun[phantomCatTest] {
		t.Error("expected test category to be marked as run after 'go test' command")
	}

	// Running a build command should mark build category as run
	s.recordToolCall("run_command", "go build ./...", false)
	if !s.categoriesRun[phantomCatBuild] {
		t.Error("expected build category to be marked as run after 'go build' command")
	}

	// A non-verification tool call should not mark any category
	s2 := newPhantomVerifyState()
	s2.recordToolCall("read_file", "/some/path/file.go", false)
	for cat := range s2.categoriesRun {
		t.Errorf("non-verification tool call should not mark category %q as run", cat)
	}
}

func TestDetectPhantomClaims_testClaimWithoutCommand(t *testing.T) {
	s := newPhantomVerifyState()
	// No verification commands run
	text := "I implemented the feature. All tests pass and the build compiles successfully."
	claims := s.detectPhantomClaims(text)

	if len(claims) == 0 {
		t.Fatal("expected phantom claims for test+build+compile assertions without verification")
	}

	cats := make(map[string]bool)
	for _, c := range claims {
		cats[c.category] = true
	}
	if !cats[phantomCatTest] {
		t.Error("expected test phantom claim")
	}
	if !cats[phantomCatCompile] {
		t.Error("expected compile phantom claim")
	}
}

func TestDetectPhantomClaims_testClaimWithCommandNotFlagged(t *testing.T) {
	s := newPhantomVerifyState()
	// Run a test command first
	s.recordToolCall("run_command", "go test ./internal/agent/", false)
	text := "All tests pass."
	claims := s.detectPhantomClaims(text)

	for _, c := range claims {
		if c.category == phantomCatTest {
			t.Errorf("test claim should not be flagged when a test command was run")
		}
	}
}

func TestDetectPhantomClaims_buildClaimWithCommandNotFlagged(t *testing.T) {
	s := newPhantomVerifyState()
	s.recordToolCall("run_command", "go build -tags goolm ./...", false)
	text := "The build passes."
	claims := s.detectPhantomClaims(text)

	for _, c := range claims {
		if c.category == phantomCatBuild {
			t.Errorf("build claim should not be flagged when a build command was run")
		}
	}
}

func TestDetectPhantomClaims_lintClaimWithoutCommand(t *testing.T) {
	s := newPhantomVerifyState()
	text := "Lint reports no issues."
	claims := s.detectPhantomClaims(text)

	found := false
	for _, c := range claims {
		if c.category == phantomCatLint {
			found = true
		}
	}
	if !found {
		t.Error("expected lint phantom claim without lint command")
	}
}

func TestDetectPhantomClaims_noClaimText(t *testing.T) {
	s := newPhantomVerifyState()
	text := "I edited the file to add the new function."
	claims := s.detectPhantomClaims(text)
	if len(claims) != 0 {
		t.Errorf("expected no phantom claims for non-assertion text, got %d", len(claims))
	}
}

func TestMaybeWarnPhantomVerify_emitsHint(t *testing.T) {
	a := &Agent{phantomVerify: newPhantomVerifyState()}
	text := "All tests pass after my changes."
	hint := a.maybeWarnPhantomVerify(text)

	if hint == "" {
		t.Fatal("expected non-empty hint for unverified test claim")
	}
	if !strings.Contains(hint, "Process Supervision") {
		t.Error("hint should mention process supervision")
	}
}

func TestMaybeWarnPhantomVerify_respectsRateLimit(t *testing.T) {
	a := &Agent{phantomVerify: newPhantomVerifyState()}
	a.phantomVerify.warnings = phantomVerifyMaxWarnings
	hint := a.maybeWarnPhantomVerify("All tests pass.")
	if hint != "" {
		t.Error("hint should be empty after rate limit reached")
	}
}

func TestMaybeWarnPhantomVerify_noClaimNoHint(t *testing.T) {
	a := &Agent{phantomVerify: newPhantomVerifyState()}
	hint := a.maybeWarnPhantomVerify("I edited the file.")
	if hint != "" {
		t.Error("hint should be empty when no verification claims present")
	}
}

func TestMaybeWarnPhantomVerify_nilStateSafe(t *testing.T) {
	a := &Agent{phantomVerify: nil}
	hint := a.maybeWarnPhantomVerify("All tests pass.")
	if hint != "" {
		t.Error("hint should be empty when phantomVerify state is nil")
	}
}

func TestPhantomVerifyState_reset(t *testing.T) {
	s := newPhantomVerifyState()
	s.recordToolCall("run_command", "go test ./...", false)
	s.warnings = 1
	s.reset()

	if len(s.categoriesRun) != 0 {
		t.Error("categoriesRun should be empty after reset")
	}
	if s.warnings != 0 {
		t.Error("warnings should be 0 after reset")
	}
}

func TestDetectPhantomClaims_dedupBySentence(t *testing.T) {
	s := newPhantomVerifyState()
	text := "All tests pass. All tests pass."
	claims := s.detectPhantomClaims(text)
	// Should dedupe to at most one claim per category per sentence
	testCount := 0
	for _, c := range claims {
		if c.category == phantomCatTest {
			testCount++
		}
	}
	if testCount > 1 {
		t.Errorf("expected at most 1 test claim after dedup, got %d", testCount)
	}
}

func TestDetectPhantomClaims_multipleCategories(t *testing.T) {
	s := newPhantomVerifyState()
	text := "The build passes. All tests pass. Lint reports no issues."
	claims := s.detectPhantomClaims(text)

	cats := make(map[string]bool)
	for _, c := range claims {
		cats[c.category] = true
	}
	if len(cats) < 2 {
		t.Errorf("expected at least 2 categories flagged, got %d", len(cats))
	}
}

func TestRecordToolCall_makeBuild(t *testing.T) {
	s := newPhantomVerifyState()
	s.recordToolCall("run_command", "make build", false)
	if !s.categoriesRun[phantomCatBuild] {
		t.Error("expected build category from 'make build'")
	}
}

func TestRecordToolCall_eslint(t *testing.T) {
	s := newPhantomVerifyState()
	s.recordToolCall("run_command", "npx eslint src/", false)
	if !s.categoriesRun[phantomCatLint] {
		t.Error("expected lint category from 'eslint' command")
	}
}

func TestRecordToolCall_pytest(t *testing.T) {
	s := newPhantomVerifyState()
	s.recordToolCall("run_command", "pytest tests/", false)
	if !s.categoriesRun[phantomCatTest] {
		t.Error("expected test category from 'pytest' command")
	}
}

func TestMaybeWarnPhantomVerify_includesCategory(t *testing.T) {
	a := &Agent{phantomVerify: newPhantomVerifyState()}
	text := "All tests pass."
	hint := a.maybeWarnPhantomVerify(text)
	if !strings.Contains(hint, "[test]") {
		t.Error("hint should include the test category label")
	}
}
