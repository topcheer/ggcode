package agent

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestIssue1165RecordersRunBeforeRuleInjection(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("cannot read agent.go: %v", err)
	}
	code := string(src)

	injectRe := regexp.MustCompile(
		`(?m)\t*result\.Content = a\.injectRulesIntoResult\(tc\.Name, tc\.Arguments, result\.Content\)`)
	injectLoc := injectRe.FindStringIndex(code)
	if injectLoc == nil {
		t.Fatal("injectRulesIntoResult assignment not found in executeToolCalls")
	}

	prefix := code[:injectLoc[0]]
	for _, rec := range []string{
		"a.overcorrectionRecordError(",
		"a.falsePremise.recordToolResult(",
		"a.integrationRecordToolResult(",
	} {
		if !strings.Contains(prefix, rec) {
			t.Errorf("%q must run before injectRulesIntoResult so injected rule text cannot poison the recorder (issues #1141, #1165)", rec)
		}
	}
	tail := code[injectLoc[0] : injectLoc[1]+400]
	if strings.Contains(strings.SplitN(tail, "\n", 20)[0]+" ", "a.falsePremise.recordToolResult(") &&
		strings.Index(code[injectLoc[0]:], "a.falsePremise.recordToolResult(") <
			len(injectRe.FindString(code)) {

		t.Error("falsePremise recorder appears after rule injection")
	}
}

func TestIssue1165InjectedRuleTextCanTripBuildClassifier(t *testing.T) {
	ruleShaped := "[Rules - learned from past mistakes]\n" +
		"Never rerun builds whose output ended with 'exit status 1'; the fix " +
		"belongs in the source file, not the command.\n"
	if !buildVerifyErrorRe.MatchString(ruleShaped) {
		t.Fatal("precondition failed: sample injected rule text no longer trips " +
			"buildVerifyErrorRe; tighten the fixture or drop this guard")
	}

	pristine := "all checks passed\nok\texample.com/internal/util\t0.4s\n"
	if buildVerifyErrorRe.MatchString(pristine) {
		t.Fatal("pristine success output should not trip buildVerifyErrorRe")
	}
}

func TestIssue1165FalsePremiseStillWorksOnPristineContent(t *testing.T) {
	var f falsePremiseState
	errOut := "util.go:27:9: undefined: Foo\nFAIL\texample.com/x [build failed]\n"
	f.recordToolResult("run_command", errOut, true)
	if len(f.recentErrors) != 1 {
		t.Fatalf("expected 1 recorded error, got %d", len(f.recentErrors))
	}

	f.recordToolResult("run_command",
		"ok  \texample.com/x\t0.401s\n", false)
	if len(f.recentErrors) != 0 {
		t.Fatalf("genuine success should clear same-tool error record, got %d entries", len(f.recentErrors))
	}

	f.recordToolResult("read_file", "package main\n\nfunc main() {}\n", false)
	if len(f.recentErrors) != 0 {
		t.Fatalf("benign successful read recorded %d error(s)", len(f.recentErrors))
	}
}
