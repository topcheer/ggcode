package agent

import (
	"testing"
)

func TestIssue607_B1_IsCriticalTagCaseInsensitive(t *testing.T) {

	for _, tag := range []string{
		"Hardcoded-Secret",
		"hardcoded-secret",
		"HARDCODED-SECRET",
		"Hardcoded-secret",
		"Path-Traversal",
		"path-traversal",
		"Git-Destructive",
		"git-destructive",
		"Pre-Commit-Build-Gate",
		"pre-commit-build-gate",
	} {
		if !isCriticalTag(tag) {
			t.Errorf("isCriticalTag(%q) = false, want true (case-insensitive match)", tag)
		}
	}

	for _, tag := range []string{
		"Hardcoded-Secret-Tip",
		"hardcoded-secrets",
		"Security-Tip",
		"Blocked-For-Now",
		"",
	} {
		if isCriticalTag(tag) {
			t.Errorf("isCriticalTag(%q) = true, want false (exact match only)", tag)
		}
	}
}

func TestIssue607_B1_BudgetBypassForPascalCriticalHint(t *testing.T) {
	var b guidanceBudget

	for i := 0; i < guidanceBudgetPerTurn; i++ {
		if !b.allow("[advisory] filler directive") {
			t.Fatalf("advisory hint %d unexpectedly suppressed before cap", i)
		}
	}

	if !b.allow("[Hardcoded-Secret] security: secret literal detected") {
		t.Error("critical Pascal-form hint suppressed after budget exhausted; want budget bypass")
	}

	if !b.allow("[CRITICAL] something went very wrong") {
		t.Error("[CRITICAL] hint suppressed after budget exhausted; want budget bypass")
	}
}

func issue607Agent() *Agent {
	a := &Agent{}
	a.guidanceBudget.reset()
	return a
}
