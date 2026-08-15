package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestIntegrationRecordAndCheck(t *testing.T) {
	s := newIntegrationState()

	// Record evidence from a grep result.
	s.recordToolEvidence("grep", "internal/agent/auth.go:42: func Authenticate(token string) error")

	if len(s.pendingEvidence) == 0 {
		t.Fatal("expected pending evidence to be recorded")
	}

	// Assistant text that references the file path and symbol.
	guidance := s.checkIntegration("Let me look at internal/agent/auth.go to fix the Authenticate function")
	if guidance != "" {
		t.Fatalf("expected no warning for good integration, got: %s", guidance)
	}
}

func TestIntegrationNotIntegratedWarning(t *testing.T) {
	s := newIntegrationState()

	s.recordToolEvidence("grep", "internal/config/loader.go:100: func LoadConfig")

	if len(s.pendingEvidence) == 0 {
		t.Fatal("expected pending evidence")
	}

	// Assistant text that doesn't reference any evidence tokens.
	guidance := s.checkIntegration("Now I will create a new file for the feature.")
	if guidance == "" {
		t.Fatal("expected integration warning, got empty")
	}

	if s.warnings != 1 {
		t.Fatalf("expected 1 warning, got %d", s.warnings)
	}
}

func TestIntegrationMaxWarnings(t *testing.T) {
	s := newIntegrationState()

	for i := 0; i < 5; i++ {
		s.recordToolEvidence("read_file", "internal/agent/agent.go:50: func FooBar")
		s.checkIntegration("unrelated text that doesn't match")
	}

	if s.warnings != integrationMaxWarnings {
		t.Fatalf("expected %d warnings (cap), got %d", integrationMaxWarnings, s.warnings)
	}
}

func TestIntegrationReset(t *testing.T) {
	s := newIntegrationState()
	s.warnings = 2
	s.pendingEvidence = []string{"test.go"}
	s.pendingTool = "grep"

	s.reset()

	if s.warnings != 0 || len(s.pendingEvidence) != 0 || s.pendingTool != "" {
		t.Fatal("reset did not clear state")
	}
}

func TestIntegrationIgnoresNonInfoTools(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("edit_file", "some content with path.go")
	if len(s.pendingEvidence) != 0 {
		t.Fatal("should not record evidence for edit_file")
	}
}

func TestIntegrationEmptyContent(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("grep", "")
	if len(s.pendingEvidence) != 0 {
		t.Fatal("should not record evidence for empty content")
	}
}

func TestIntegrationNoPendingEvidence(t *testing.T) {
	s := newIntegrationState()
	guidance := s.checkIntegration("some text")
	if guidance != "" {
		t.Fatal("expected empty guidance when no pending evidence")
	}
}

func TestIntegrationEmptyAssistantText(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("grep", "internal/agent/auth.go:42: func Authenticate")
	guidance := s.checkIntegration("")
	if guidance != "" {
		t.Fatal("expected empty guidance for empty assistant text")
	}
}

func TestIntegrationNoiseFiltered(t *testing.T) {
	s := newIntegrationState()
	// Content with mostly noise tokens.
	s.recordToolEvidence("grep", "go.mod: package main\nimport foo\nreturn error")
	if len(s.pendingEvidence) != 0 {
		t.Fatalf("expected noise to be filtered, got: %v", s.pendingEvidence)
	}
}

func TestIntegrationLineRefIntegration(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("lsp_diagnostics", "auth.go line 142: undefined variable\nconfig.go line 50: type mismatch")

	if len(s.pendingEvidence) < 2 {
		t.Fatalf("expected at least 2 evidence tokens, got %d", len(s.pendingEvidence))
	}

	// Text referencing line numbers should count as integration.
	guidance := s.checkIntegration("I see the issue at line 142, let me fix it")
	if guidance != "" {
		t.Fatalf("expected no warning for line ref integration, got: %s", guidance)
	}
}

func TestIntegrationPartialIntegrationOK(t *testing.T) {
	s := newIntegrationState()
	// Many evidence tokens but agent references at least 1/3.
	s.recordToolEvidence("read_file", "internal/agent/agent.go:42: func Run\ninternal/agent/loop.go:100: func Loop\ninternal/agent/util.go:50: func Helper")

	if len(s.pendingEvidence) < 2 {
		t.Fatalf("expected at least 2 evidence tokens, got %d", len(s.pendingEvidence))
	}

	// Reference at least one file path.
	guidance := s.checkIntegration("I'll modify internal/agent/agent.go first")
	if guidance != "" {
		t.Fatalf("expected no warning for partial integration meeting threshold, got: %s", guidance)
	}
}

// --- issue #345 regression tests: "doing" over "saying" ---

// TestIntegrationActThenSummarizeNoFlag covers the canonical non-false-positive
// flow: read evidence -> edit_file acting on that file -> summary that never
// echoes the full path. Must NOT trigger a warning.
func TestIntegrationActThenSummarizeNoFlag(t *testing.T) {
	s := newIntegrationState()

	s.recordToolEvidence("read_file", "internal/agent/auth.go:42: func Authenticate(token string) error")
	if len(s.pendingEvidence) == 0 {
		t.Fatal("expected pending evidence to be recorded")
	}

	// edit_file result whose content contains the target path (as in
	// "Replaced 1 occurrence in <path>: ..." / "[Changes]" output).
	s.recordToolEvidence("edit_file",
		"Replaced 1 occurrence in internal/agent/auth.go: 3 lines -> 5 lines\n\n[Changes]\n+ token, err := parseToken(tok)")

	if len(s.pendingEvidence) != 0 {
		t.Fatalf("expected evidence consumed by mutating tool, still pending: %v", s.pendingEvidence)
	}

	// Summary without any full path must not warn.
	if guidance := s.checkIntegration("Fixed the token validation issue in the auth flow."); guidance != "" {
		t.Fatalf("expected no warning for act-then-summarize flow, got: %s", guidance)
	}
}

// TestIntegrationBaseNameMentionsCount verifies relaxed text matching: the
// file's base name (not the full path) suffices as integration.
func TestIntegrationBaseNameMentionsCount(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("grep", "internal/config/loader.go:100: func LoadConfig")

	if guidance := s.checkIntegration("The problem is in loader.go -- I'll fix LoadConfig next."); guidance != "" {
		t.Fatalf("expected no warning when base name is mentioned, got: %s", guidance)
	}
}

// TestIntegrationSingleEvidenceFileName verifies the n==1 relaxed rule: a
// single evidence token counts as integrated when the related file name is
// mentioned.
func TestIntegrationSingleEvidenceFileName(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("lsp_definition", "internal/tool/edit_file.go:174: msg += compactDiff(content, string(writeData))")

	if len(s.pendingEvidence) == 0 {
		t.Fatal("expected pending evidence")
	}

	if guidance := s.checkIntegration("Found it -- the diff is appended in edit_file.go, I'll adjust that."); guidance != "" {
		t.Fatalf("expected no warning for single-evidence base-name mention, got: %s", guidance)
	}
}

// TestIntegrationPackageNameMentionsCount verifies package-name (directory
// base) mentions count as integration.
func TestIntegrationPackageNameMentionsCount(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("grep", "internal/tool/edit_file.go:174: func compactDiff")

	if guidance := s.checkIntegration("Looks like the tool package already builds the diff, so no re-read needed."); guidance != "" {
		t.Fatalf("expected no warning when package name is mentioned, got: %s", guidance)
	}
}

// TestIntegrationStillFiresWhenUnused verifies the detector still fires when
// evidence is entirely unused and no action followed.
func TestIntegrationStillFiresWhenUnused(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("grep", "internal/config/loader.go:100: func LoadConfig")

	if guidance := s.checkIntegration("Now I will create a new file for the feature."); guidance == "" {
		t.Fatal("expected integration warning for unused evidence, got empty")
	}
}

// TestIntegrationEvidenceAppendedNotOverwritten verifies consecutive
// retrieval calls accumulate evidence instead of overwriting.
func TestIntegrationEvidenceAppendedNotOverwritten(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("read_file", "internal/agent/auth.go:10: func Login")
	n1 := len(s.pendingEvidence)
	if n1 == 0 {
		t.Fatal("expected evidence after first read_file")
	}

	s.recordToolEvidence("grep", "internal/config/loader.go:100: func LoadConfig")
	n2 := len(s.pendingEvidence)
	if n2 <= n1 {
		t.Fatalf("expected evidence appended (%d -> %d), got overwrite", n1, n2)
	}

	foundAuth, foundLoader := false, false
	for _, ev := range s.pendingEvidence {
		if strings.Contains(ev, "auth.go") {
			foundAuth = true
		}
		if strings.Contains(ev, "loader.go") {
			foundLoader = true
		}
	}
	if !foundAuth || !foundLoader {
		t.Fatalf("expected both files' evidence preserved, got: %v", s.pendingEvidence)
	}
}

// TestIntegrationEvidenceCap verifies the pending evidence cap.
func TestIntegrationEvidenceCap(t *testing.T) {
	s := newIntegrationState()
	for i := 0; i < 6; i++ {
		s.recordToolEvidence("read_file", fmt.Sprintf("internal/pkg/file%02d.go:10: func Fn%02d", i, i))
	}
	if len(s.pendingEvidence) > integrationMaxPendingEvidence {
		t.Fatalf("expected cap of %d, got %d", integrationMaxPendingEvidence, len(s.pendingEvidence))
	}
}

// TestIntegrationPathListExcluded verifies pure path-list browsing output
// (e.g. grep files_with_matches) does not become must-echo evidence.
func TestIntegrationPathListExcluded(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("grep", "internal/agent/auth.go\ninternal/agent/agent.go\ninternal/config/loader.go")

	if len(s.pendingEvidence) != 0 {
		t.Fatalf("expected path-list output excluded from evidence, got: %v", s.pendingEvidence)
	}
}

// TestIntegrationEvidenceExpiresAfterUnrelatedMutation verifies the
// penetration limit: pending evidence that a mutating tool did NOT consume
// still expires instead of surviving to later summaries.
func TestIntegrationEvidenceExpiresAfterUnrelatedMutation(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("grep", "internal/config/loader.go:100: func LoadConfig")
	if len(s.pendingEvidence) == 0 {
		t.Fatal("expected pending evidence")
	}

	// Mutating tool whose target hits nothing pending.
	s.recordToolEvidence("edit_file", "Replaced 1 occurrence in internal/other/thing.go: 1 lines -> 2 lines")

	if len(s.pendingEvidence) != 0 {
		t.Fatalf("expected evidence to expire after unrelated mutation, still pending: %v", s.pendingEvidence)
	}

	if guidance := s.checkIntegration("Done with the fix."); guidance != "" {
		t.Fatalf("expected no warning after evidence expiry, got: %s", guidance)
	}
}
