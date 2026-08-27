package agent

// Regression tests for GitHub issues #1119, #1120, #1124 against
// internal/agent/logging_intel_check.go.
//
//	#1119: old/new delta keys must anchor on position-insensitive content
//	       (normalized call text), not line numbers; inserting a comment line
//	       above retained instances must not re-flag them.
//	#1120: goLogCallRe must not anchor on the start of a physical line;
//	       log.Printf / defer log.Printf / single-line if bodies /
//	       semicolon-chained calls must all be detected.
//	#1124: stripInitFuncs must skip string literals during brace balancing so
//	       a '}' or '{' inside a message cannot early-terminate the scan,
//	       wedge the counter past EOF, or swallow genuine log.Fatal calls
//	       after the init body.

import (
	"strings"
	"testing"
)

// ---------- #1119 ----------

const issue1119Base = `package auth

import "log"

func handler(req string) {
	token := getToken()
	log.Printf("req=%s token=%s", req, token)
}
`

func issue1119WithCommentsAbove(n int, aboveImport bool) string {
	s := issue1119Base
	insertion := strings.Repeat("\t// audit note\n", n)
	if aboveImport {
		return strings.Replace(s, "import \"log\"", insertion+"import \"log\"", 1)
	}
	return strings.Replace(s, "\ttoken := getToken()", insertion+"\ttoken := getToken()", 1)
}

func findGoSensitiveInst(t *testing.T, src string) loggingIntelInstance {
	t.Helper()
	insts := findGoSensitiveLogArgs(src)
	if len(insts) == 0 {
		t.Fatalf("expected sensitive-log instance in:\n%s", src)
	}
	return insts[0]
}

func TestIssue1119InsertCommentAboveNoRefire(t *testing.T) {
	oldWarnings := checkLoggingIntel("internal/auth/handler.go", "", issue1119Base)
	if len(oldWarnings) != 1 {
		t.Fatalf("baseline: want exactly 1 warning, got %d: %v", len(oldWarnings), oldWarnings)
	}
	for _, tc := range []struct {
		name        string
		newContent  string
		aboveImport bool
	}{
		{"one comment above call", issue1119WithCommentsAbove(1, false), false},
		{"five comments above call", issue1119WithCommentsAbove(5, false), false},
		{"comment above import shifts everything", issue1119WithCommentsAbove(3, true), true},
	} {
		warnings := checkLoggingIntel("internal/auth/handler.go", issue1119Base, tc.newContent)
		if len(warnings) != 0 {
			t.Errorf("%s: insert-only edit must not refire; got %v", tc.name, warnings)
		}
	}
}

func TestIssue1119ChangedArgStillFlags(t *testing.T) {
	// Guard against over-normalization: changing the sensitive identifier is a
	// semantic change and must still surface as a new instance.
	newContent := strings.Replace(issue1119Base,
		"log.Printf(\"req=%s token=%s\", req, token)",
		"log.Printf(\"req=%s password=%s\", req, password)", 1)
	warnings := checkLoggingIntel("internal/auth/handler.go", issue1119Base, newContent)
	if len(warnings) != 1 {
		t.Fatalf("changed sensitive arg must re-flag; got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "sensitive") {
		t.Fatalf("expected sensitive_log_arg warning, got: %s", warnings[0])
	}
}

func TestIssue1119KeyIgnoresLineAndFormatting(t *testing.T) {
	base := "package lib\n\nimport \"log\"\n\nfunc f(secret string) {\n" +
		"\tlog.Printf(\"s=%s\", secret)\n}\n"
	a := findGoSensitiveInst(t, base)
	b := findGoSensitiveInst(t, strings.Replace(base, "func f(", "// note\nfunc f(", 1))
	if normalizeLogCallKey(a) != normalizeLogCallKey(b) {
		t.Fatalf("keys must be line-independent:\n%q\nvs\n%q",
			normalizeLogCallKey(a), normalizeLogCallKey(b))
	}
	// Whitespace-only formatting churn: spacing outside and inside the call
	// (including inside the format string) must not split identity.
	c := findGoSensitiveInst(t, strings.Replace(base,
		"log.Printf(\"s=%s\", secret)", "log.Printf( \"s = %s\" , secret )", 1))
	if normalizeLogCallKey(c) != normalizeLogCallKey(a) {
		t.Fatalf("whitespace-only churn must not split identity:\n%q\nvs\n%q",
			normalizeLogCallKey(c), normalizeLogCallKey(a))
	}
	// A different identifier is a real change.
	d := findGoSensitiveInst(t, strings.Replace(base, "\"s=%s\", secret", "\"s=%s\", password", 1))
	if normalizeLogCallKey(d) == normalizeLogCallKey(a) {
		t.Fatal("different sensitive identifier must produce a different key")
	}
	// A semantically different message is also a real change (#1109B parity).
	e := findGoSensitiveInst(t, strings.Replace(base, "\"s=%s\"", "\"user login failed\"", 1))
	if normalizeLogCallKey(e) == normalizeLogCallKey(a) {
		t.Fatal("different log message text must produce a different key")
	}
	// Fatal-family instances are keyed by their normalized call text:
	// identical texts at any line share one identity; differing texts do not.
	fSrcA := "package lib\n\nimport \"log\"\n\nfunc a() {\n\tlog.Fatal(\"x\")\n}\n"
	f1 := findFatalInLibForTest(t, fSrcA, "internal/lib/a.go")
	f2 := findFatalInLibForTest(t, "package lib\n\nimport \"log\"\n\n\n\nfunc a() {\n\tlog.Fatal(\"x\")\n}\n\n\n\nfunc b() {}\n", "internal/lib/b.go")
	if normalizeLogCallKey(f1) != normalizeLogCallKey(f2) {
		t.Fatal("fatal instances with identical text at different lines must share one key")
	}
	f3 := findFatalInLibForTest(t, strings.Replace(fSrcA, "\"x\"", "\"y\"", 1), "internal/lib/c.go")
	if normalizeLogCallKey(f3) == normalizeLogCallKey(f1) {
		t.Fatal("fatal instances with different messages must differ in key")
	}
}

func findFatalInLibForTest(t *testing.T, src, filePath string) loggingIntelInstance {
	t.Helper()
	insts := findFatalInLib(src, ".go", filePath)
	if len(insts) == 0 {
		t.Fatalf("expected fatal_in_library instance in:\n%s", src)
	}
	return insts[0]
}

// ---------- #1120 ----------

const issue1120Scaffold = "package lib\n\nimport \"log\"\n\nvar doWork = func() {}\n\n" +
	"func hit(secret string) {\n"

func runIssue1120Case(t *testing.T, name, body string) {
	t.Helper()
	content := issue1120Scaffold + body + "\n}\n"
	warnings := checkLoggingIntel("internal/lib/hit.go", "", content)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "sensitive") {
			found = true
		}
	}
	if !found {
		t.Errorf("%s: expected a sensitive_log_arg warning among %v", name, warnings)
	}
}

func TestIssue1120NonStatementLeadingFormsDetected(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"deferred call", "\tdefer log.Printf(\"done secret=%s\", secret)"},
		{"single-line if body", "\tif secret != \"\" { log.Printf(\"leaked=%s\", secret) }"},
		{"semicolon chain", "\tdoWork(); log.Printf(\"after secret=%s\", secret)"},
	} {
		runIssue1120Case(t, tc.name, tc.body)
	}
}

func TestIssue1120RegexUnit(t *testing.T) {
	mustMatch := []string{
		"log.Printf(\"a=%s\", tok)",
		"\t\tlogger.Info(\"hello\")",
		"defer log.Println(x)",
		"if err != nil { slog.Warn(err.Error()) }",
		"w(); logr.Log()",
	}
	for _, s := range mustMatch {
		if !goLogCallRe.MatchString(s) {
			t.Errorf("goLogCallRe must match: %q", s)
		}
	}
	mustNotMatch := []string{
		"catalogAPI.Print(\"x\")",
		"xlog.Info(\"y\")",
		"zapSugar.Infow()",
		"fmt.Sprintf(\"%s\", tok)",
	}
	for _, s := range mustNotMatch {
		if goLogCallRe.MatchString(s) {
			t.Errorf("goLogCallRe must not match receiver-tail/other-package idents: %q", s)
		}
	}
}

func TestIssue1120CatalogIdentifierNoFalsePositive(t *testing.T) {
	content := "package lib\n\nimport \"log\"\n\nvar catalogData = map[string]int{}\n\n" +
		"func look(secret string) {\n\t_ = catalogData\n\t_ = wordsCatalogTokenizer(secret)\n}\n"
	warnings := checkLoggingIntel("internal/lib/look.go", "", content)
	if len(warnings) != 0 {
		t.Fatalf("receiver-tail identifiers must stay silent; got %v", warnings)
	}
}

// ---------- #1124 ----------

func TestIssue1124BraceInsideMessageNoEarlyExit(t *testing.T) {
	// FP-A shape: a '}' inside the string previously closed the depth counter
	// immediately, leaking the rest of the init body (with its own Fatal)
	// back into the analyzed source as a false positive.
	raw := "func init() { m := \"}\"; if bad { log.Fatal(\"init config broken\") }\n}\nfunc reg() {}\n"
	got := stripInitFuncs(raw)
	want := "\nfunc reg() {}\n"
	if got != want {
		t.Fatalf("FP-A strip mismatch:\ngot:  %q\nwant: %q", got, want)
	}
	if warnings := checkLoggingIntel("internal/lib/cfg.go", "", raw); len(warnings) != 0 {
		t.Fatalf("FP-A: init-body fatal must be stripped (no warnings); got %v", warnings)
	}
}

func TestIssue1124OpenBraceInsideMessageNoWedge(t *testing.T) {
	// FP-B shape: '{' inside the string previously pushed depth permanently
	// above zero, running off end-of-file and swallowing everything after the
	// init - including genuine log.Fatal calls in later functions.
	raw := "func init() { t := \"template{root={x}}\"; setup(t)\n}\n" +
		"func reg() { log.Fatal(\"should flag\") }\n"
	got := stripInitFuncs(raw)
	want := "\nfunc reg() { log.Fatal(\"should flag\") }\n"
	if got != want {
		t.Fatalf("FP-B strip mismatch:\ngot:  %q\nwant: %q", got, want)
	}
	warnings := checkLoggingIntel("internal/lib/tmpl.go", "", raw)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Fatal") {
		t.Fatalf("FN guard: genuine fatal after wedged init must surface; got %v", warnings)
	}
}

func TestIssue1124EscapedQuoteThenBraceStripsCleanly(t *testing.T) {
	raw := "func init() { s := \"a\\\"}\"; work(s) }\nfunc keeper() { log.Fatal(\"keeper fail\") }\n"
	got := stripInitFuncs(raw)
	want := "\nfunc keeper() { log.Fatal(\"keeper fail\") }\n"
	if got != want {
		t.Fatalf("escaped-quote strip mismatch:\ngot:  %q\nwant: %q", got, want)
	}
	warnings := checkLoggingIntel("internal/lib/k.go", "", raw)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Fatal") {
		t.Fatalf("escaped-literal case: keeper fatal must be flagged once; got %v", warnings)
	}
}

func TestIssue1124UnterminatedStringLeavesSourceUntouched(t *testing.T) {
	// Unterminated raw string inside init: scanner cannot know where the body
	// ends, so the whole file is left untouched (same lenient contract as the
	// unterminated-init test in logging_intel_check_test.go).
	raw := "func init() { r := `raw { open \n\tkeepGoing() }\nfunc tail() { log.Fatal(\"tail\") }"
	got := stripInitFuncs(raw)
	if got != raw {
		t.Fatalf("unterminated literal must leave source untouched;\ngot:  %q\nwant: %q", got, raw)
	}
}

func TestIssue1124MultiInitStillAllStripped(t *testing.T) {
	// Cross-check that the string-aware loop did not regress multi-init
	// handling from logging_intel_check_test.go.
	raw := "func init() { a := \"}\"; x() }\nfunc init() { b := \"{\"; y() }\n" +
		"func reg() { log.Fatal(\"flagged\") }\n"
	got := stripInitFuncs(raw)
	if strings.Contains(got, "init()") || strings.Contains(got, "x()") || strings.Contains(got, "y()") {
		t.Fatalf("both init bodies must be stripped; got %q", got)
	}
	if !strings.Contains(got, "log.Fatal(\"flagged\")") {
		t.Fatalf("regular function must survive stripping; got %q", got)
	}
}
