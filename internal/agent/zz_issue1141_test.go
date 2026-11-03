package agent

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestIssue1141BenignProseIsNotDiagnostic(t *testing.T) {
	benign := []string{

		"We prefer using the Makefile for all build targets.",

		"config should be loaded before use",

		"Consider restarting your shell to pick up the updated PATH.",

		"# Title\n\n## Warning: behavior changed\n\nSee documentation.",
		"Changes:\n2. Warning: defaults differ from v1\n",

		"Docs prefer listing packages such as fmt or internal/util in examples.",
	}
	for _, out := range benign {
		if sev := classifyDiagnosticSeverity("run_command", out); sev != severityNone {
			t.Errorf("benign output classified as %v, want none: %q", sev, out)
		}
	}

	if sev := classifyDiagnosticSeverity("read_file", "docs prefer long-form usage notes"); sev != severityNone {
		t.Errorf("non-command tool classified as %v, want none", sev)
	}
}

func TestIssue1141AnchoredWarningsStillDetected(t *testing.T) {
	anchored := []string{
		"util.go:27:2: warning: unused variable x",
		"/path/to/main.c:41:9: warning: implicit declaration of function 'f'",
		"internal/agent/a.go:120:3: Warning: field alignment could be improved",
		"src/app.ts:88:19: warning: unexpected any, specify a different type",

		"golangci-lint run...\nwarning: G104: errors unhandled",
	}
	for _, out := range anchored {
		if sev := classifyDiagnosticSeverity("run_command", out); sev != severityTrivial {
			t.Errorf("anchored diagnostic classified as %v, want trivial: %q", sev, out)
		}
	}

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

func TestIssue1141BenignOutputDoesNotPoisonLargeEdit(t *testing.T) {
	s := newOvercorrectionState()
	s.recordErrorSignal("run_command",
		"RELEASE NOTES\nWe prefer using the Makefile.\nAll checks passed.\n", false)
	if hint := s.recordEdit(6000, "big_but_legal_refactor.go"); hint != "" {
		t.Fatalf("benign prose leaked into overcorrection verdict: %q", hint)
	}
}

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

func TestIssue1141RecorderRunsBeforeRuleInjection(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("cannot read agent.go: %v", err)
	}
	code := string(src)

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
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
