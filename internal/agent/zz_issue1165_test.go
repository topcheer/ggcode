package agent

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Regression tests for GitHub issue #1165.
//
// Issue #1141 established the invariant that tool-result recorders must run
// BEFORE injectRulesIntoResult rewrites result.Content: injected learned-rule
// text must never enter diagnostic classifiers. Only overcorrectionRecordError
// was moved, while two sibling recorders on the same code path still consumed
// the injected (prefixed) content:
//
//   - falsePremise.recordToolResult: rule text naturally contains build-fail /
//     exit-status phrasing, so buildVerifyErrorRe misclassified it and skewed
//     isBuildTestError handling plus the #546/#593 supersede logic;
//   - integrationRecordToolResult: extractEvidenceTokens mines rule-text paths
//     and symbols as "evidence" the next assistant turn never echoes, and the
//     injected header evicts real content from the evidence window.
//
// This file pins:
//  1. structural ordering of both recorders before injection (#1165);
//  2. the hazard itself: injected rule text DOES trip buildVerifyErrorRe when
//     it carries failure phrasing, so feeding recorders pre-injection content
//     is load-bearing;
//  3. falsePremise still behaves correctly on pristine content after the move.

// TestIssue1165RecordersRunBeforeRuleInjection pins call ordering inside
// executeToolCalls: overcorrectionRecordError (#1141), then
// falsePremise.recordToolResult and integrationRecordToolResult (#1165), all
// strictly before the injectRulesIntoResult rewrite.
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
		"a.overcorrectionRecordError(",     // #1141
		"a.falsePremise.recordToolResult(", // #1165
		"a.integrationRecordToolResult(",   // #1165
	} {
		if !strings.Contains(prefix, rec) {
			t.Errorf("%q must run before injectRulesIntoResult so injected rule text cannot poison the recorder (issues #1141, #1165)", rec)
		}
	}
	tail := code[injectLoc[0] : injectLoc[1]+400]
	if strings.Contains(strings.SplitN(tail, "\n", 20)[0]+" ", "a.falsePremise.recordToolResult(") &&
		strings.Index(code[injectLoc[0]:], "a.falsePremise.recordToolResult(") <
			len(injectRe.FindString(code)) {
		// Defensive double check: recorder must not appear immediately after
		// the injection statement either.
		t.Error("falsePremise recorder appears after rule injection")
	}
	if !strings.Contains(prefix[max(0, len(prefix)-500):], "#1165") {
		t.Error("call-order comment should reference issue #1165 so the constraint survives refactors")
	}
}

// TestIssue1165InjectedRuleTextCanTripBuildClassifier documents the hazard:
// learned-rule guidance text phrased like tool failures matches
// buildVerifyErrorRe once it lands inside recorded snippets. This is exactly
// why the recorders above consume PRISTINE content.
func TestIssue1165InjectedRuleTextCanTripBuildClassifier(t *testing.T) {
	ruleShaped := "[Rules - learned from past mistakes]\n" +
		"Never rerun builds whose output ended with 'exit status 1'; the fix " +
		"belongs in the source file, not the command.\n"
	if !buildVerifyErrorRe.MatchString(ruleShaped) {
		t.Fatal("precondition failed: sample injected rule text no longer trips " +
			"buildVerifyErrorRe; tighten the fixture or drop this guard")
	}
	// Benign pristine success output stays clean for contrast.
	pristine := "all checks passed\nok\texample.com/internal/util\t0.4s\n"
	if buildVerifyErrorRe.MatchString(pristine) {
		t.Fatal("pristine success output should not trip buildVerifyErrorRe")
	}
}

// TestIssue1165FalsePremiseStillWorksOnPristineContent proves the reordered
// recorder remains fully functional: an error creates a record and a later
// genuine same-tool success with plain pristine content clears it (#331).
func TestIssue1165FalsePremiseStillWorksOnPristineContent(t *testing.T) {
	var f falsePremiseState
	errOut := "util.go:27:9: undefined: Foo\nFAIL\texample.com/x [build failed]\n"
	f.recordToolResult("run_command", errOut, true)
	if len(f.recentErrors) != 1 {
		t.Fatalf("expected 1 recorded error, got %d", len(f.recentErrors))
	}
	// Pristine go-test success output ("ok  <pkg>  0.4s") matches
	// buildSuccessRe's ^ok anchor, so the genuine-success supersede path
	// fires and clears the record.
	f.recordToolResult("run_command",
		"ok  \texample.com/x\t0.401s\n", false)
	if len(f.recentErrors) != 0 {
		t.Fatalf("genuine success should clear same-tool error record, got %d entries", len(f.recentErrors))
	}
	// A benign non-error tool result with no prior records records nothing.
	f.recordToolResult("read_file", "package main\n\nfunc main() {}\n", false)
	if len(f.recentErrors) != 0 {
		t.Fatalf("benign successful read recorded %d error(s)", len(f.recentErrors))
	}
}
