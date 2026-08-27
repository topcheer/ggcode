package agent

// Regression tests for GitHub issue #1141.
//
// classifyDiagnosticSeverity previously ran lowercase substring matching over
// SUCCESSFUL run_command/start_command output with loosely-worded patterns
// ("prefer", "consider", "should be", bare "warning:"). Benign output -
// README prose, test logs, install hints - set pendingErr = trivial, and the
// next legitimate >=5KB edit then produced a bogus
// "[overcorrection-cascade] Fix for trivial error was N bytes - too large"
// warning. A second channel self-infected results: agent.go called
// injectRulesIntoResult BEFORE the recorder, so injected learned-rule text
// could re-classify as a diagnostic and re-trigger itself.
//
// This file pins:
//  1. benign prose no longer classifies as a command diagnostic;
//  2. genuinely formatted warnings (positional / line-anchored) still do;
//  3. end-to-end: benign output plus a huge edit fires no warning;
//  4. end-to-end: a real anchored warning plus a huge edit still guards;
//  5. structural ordering: the recorder runs before rule injection (#1141).

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestIssue1141BenignProseIsNotDiagnostic reproduces each benign-output class
// named in issue #1141 and asserts classifyDiagnosticSeverity returns none.
func TestIssue1141BenignProseIsNotDiagnostic(t *testing.T) {
	benign := []string{
		// README-style sentence.
		"We prefer using the Makefile for all build targets.",
		// Test-log style assertion message.
		"config should be loaded before use",
		// Installer completion hint.
		"Consider restarting your shell to pick up the updated PATH.",
		// Changelog heading, embedded mid-content without diagnostic anchors.
		"# Title\n\n## Warning: behavior changed\n\nSee documentation.",
		"Changes:\n2. Warning: defaults differ from v1\n",
		// Prose mention near filenames, without a file:line:col diagnostic shape.
		"Docs prefer listing packages such as fmt or internal/util in examples.",
	}
	for _, out := range benign {
		if sev := classifyDiagnosticSeverity("run_command", out); sev != severityNone {
			t.Errorf("benign output classified as %v, want none: %q", sev, out)
		}
	}
	// Non-command tool results were always exempt; keep them that way (#1141).
	if sev := classifyDiagnosticSeverity("read_file", "docs prefer long-form usage notes"); sev != severityNone {
		t.Errorf("non-command tool classified as %v, want none", sev)
	}
}

// TestIssue1141AnchoredWarningsStillDetected verifies the format anchoring
// introduced by #1141 keeps matching REAL compiler/linter warning shapes.
func TestIssue1141AnchoredWarningsStillDetected(t *testing.T) {
	anchored := []string{
		"util.go:27:2: warning: unused variable x",
		"/path/to/main.c:41:9: warning: implicit declaration of function 'f'",
		"internal/agent/a.go:120:3: Warning: field alignment could be improved",
		"src/app.ts:88:19: warning: unexpected any, specify a different type",
		// Bare-line form emitted by some linters/installers.
		"golangci-lint run...\nwarning: G104: errors unhandled",
	}
	for _, out := range anchored {
		if sev := classifyDiagnosticSeverity("run_command", out); sev != severityTrivial {
			t.Errorf("anchored diagnostic classified as %v, want trivial: %q", sev, out)
		}
	}
	// Legacy unambiguous linter phrases stay matched.
	keep := []string{
		"x declared and not used: x",
		"y declared but not used",
		"a.go:4:2: lint: comment on exported function Foo",
		"Use staticcheck to find additional issues.",
		"flag deprecated: use X instead",
		"a.go:8:9: warning: unused variable z",
	}
	for _, out := range keep {
		if sev := classifyDiagnosticSeverity("run_command", out); sev != severityTrivial {
			t.Errorf("kept linter pattern classified as %v, want trivial: %q", sev, out)
		}
	}
}

// TestIssue1141BenignOutputDoesNotPoisonLargeEdit replays the core failure
// from #1141: benign successful output records no pending error, so a later
// 6000-byte legal edit produces NO overcorrection guidance.
func TestIssue1141BenignOutputDoesNotPoisonLargeEdit(t *testing.T) {
	s := newOvercorrectionState()
	s.recordErrorSignal("run_command",
		"RELEASE NOTES\nWe prefer using the Makefile.\nAll checks passed.\n", false)
	if hint := s.recordEdit(6000, "big_but_legal_refactor.go"); hint != "" {
		t.Fatalf("benign prose leaked into overcorrection verdict: %q", hint)
	}
}

// TestIssue1141RealDiagnosticStillGuardsLargeEdit proves the guard itself is
// alive: a positionally-anchored warning sets pendingErr=trivial and the same
// oversized edit IS flagged.
func TestIssue1141RealDiagnosticStillGuardsLargeEdit(t *testing.T) {
	s := newOvercorrectionState()
	s.recordErrorSignal("run_command",
		"util.go:27:2: warning: unused variable x\nok  \texample.com/pkg\t0.3s\n", false)
	hint := s.recordEdit(6000, "util.go")
	want := fmt.Sprintf(
		"[overcorrection-cascade] Fix for trivial error was %d bytes - too large",
		6000)
	if !strings.Contains(hint, want) {
		t.Fatalf("real anchored warning should still guard a 6000-byte edit, got %q", hint)
	}
}

// TestIssue1141RecorderRunsBeforeRuleInjection pins the call-order fix inside
// executeToolCalls: overcorrectionRecordError must consume the PRISTINE
// result.Content, i.e. appear strictly before the
// result.Content = a.injectRulesIntoResult(...) rewrite that prefixes learned
// rules onto the visible payload (#1141).
//
// Implementation detail: the detector's final state machine wiring lives in
// internal/agent/agent.go inside executeToolCalls, while the implementation
// under test here lives in overcorrection_cascade.go, so the source is read
// directly instead of going through lsp_document_highlights.
func TestIssue1141RecorderRunsBeforeRuleInjection(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("cannot read agent.go: %v", err)
	}
	code := string(src)

	// The injection-statement shape, unique within executeToolCalls.
	injectRe := regexp.MustCompile(`(?m)^\t*result\.Content = a\.injectRulesIntoResult\(tc\.Name, tc\.Arguments, result\.Content\)$`)
	injectLoc := injectRe.FindStringIndex(code)
	if injectLoc == nil {
		t.Fatal("injectRulesIntoResult assignment not found in executeToolCalls")
	}
	recName := "a.overcorrectionRecordError("
	recIdx := strings.LastIndex(code[:injectLoc[0]], recName)
	if recIdx < 0 {
		t.Fatal("overcorrectionRecordError must run before injectRulesIntoResult so injected rule text cannot self-trigger the recorder (#1141)")
	}
	recent := code[maxInt(0, recIdx-240):recIdx]
	if !strings.Contains(recent, "#1141") {
		t.Error("call-order comment should reference issue #1141 so the constraint survives refactors")
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
