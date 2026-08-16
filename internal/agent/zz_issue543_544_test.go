package agent

// zz_issue543_544_test.go — feature tests for issues #543 and #544.
//
// #543:
//   - SetSessionTokenBudget must actually store the budget (was an empty
//     body end-to-end no-op) and the getter/enforcement primitive must work.
//   - ApplyToolCallBudget must propagate 0 too, so a config reload that
//     removes tool_call_budget resets a previously applied value (tested in
//     internal/agentruntime/zz_issue543_544_test.go alongside the runtime
//     half of the fix).
//
// #544:
//   - spec_gaming Pattern 2 must not fire on read-only investigative
//     commands (grep "t.Skip(" ...), and Pattern 1 must be exempt when the
//     task itself is writing tests.
//   - wt_invalidation must classify read-only git invocations
//     (git_stash list / `git stash list` in a command field / `git status`
//     etc.) as non-mutating, and still warn for real mutations.
//   - debug_stmt must not flag a comment that merely MENTIONS a debug call
//     (// removed fmt.Println(...) below), while still flagging a real
//     newly-introduced call.

import (
	"strings"
	"testing"
)

// ---------- #543: SetSessionTokenBudget storage ----------

func TestIssue543SetSessionTokenBudgetStoresValue(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	if got := ag.SessionTokenBudget(); got != 0 {
		t.Fatalf("initial budget: want 0, got %d", got)
	}
	ag.SetSessionTokenBudget(123456)
	if got := ag.SessionTokenBudget(); got != 123456 {
		t.Fatalf("after SetSessionTokenBudget(123456): want 123456, got %d", got)
	}
	// 0 clears enforcement.
	ag.SetSessionTokenBudget(0)
	if got := ag.SessionTokenBudget(); got != 0 {
		t.Fatalf("after SetSessionTokenBudget(0): want 0, got %d", got)
	}
}

func TestIssue543RecordSessionTokenUsageThresholds(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	ag.SetSessionTokenBudget(1000)

	// 50%: silent.
	msg, stop := ag.RecordSessionTokenUsage(400, 100)
	if msg != "" || stop {
		t.Fatalf("at 50%%: want silent, got msg=%q stop=%v", msg, stop)
	}
	// 80%: first warning.
	msg, stop = ag.RecordSessionTokenUsage(200, 100)
	if msg == "" || stop {
		t.Fatalf("at 80%%: want warning, got msg=%q stop=%v", msg, stop)
	}
	if !strings.Contains(msg, "80%") {
		t.Fatalf("80%% warning should mention the percentage: %q", msg)
	}
	// Still under 95%: no repeat fire.
	msg, stop = ag.RecordSessionTokenUsage(1, 0)
	if msg != "" || stop {
		t.Fatalf("between thresholds: want silent, got msg=%q stop=%v", msg, stop)
	}
	// 95-99% (950/1000): urgent warning, not yet a stop.
	msg, stop = ag.RecordSessionTokenUsage(50, 100)
	if msg == "" || stop {
		t.Fatalf("at 95%%: want urgent warning without stop, got msg=%q stop=%v", msg, stop)
	}
	if !strings.Contains(msg, "95%") {
		t.Fatalf("95%% warning should mention the percentage: %q", msg)
	}
	// Crossing to 100%+: hard stop.
	msg, stop = ag.RecordSessionTokenUsage(50, 100)
	if msg == "" || !stop {
		t.Fatalf("at 100%%+: want hard stop, got msg=%q stop=%v", msg, stop)
	}
	if !strings.Contains(msg, "exhausted") {
		t.Fatalf("hard-stop message should say exhausted: %q", msg)
	}

	// No budget configured: never stops.
	ag2 := NewAgent(nil, nil, "sys", 5)
	defer ag2.Close()
	msg, stop = ag2.RecordSessionTokenUsage(1_000_000, 1_000_000)
	if msg != "" || stop {
		t.Fatalf("no budget: want silent non-stop, got msg=%q stop=%v", msg, stop)
	}
}

// ---------- #544: spec_gaming ----------

func TestIssue544SpecGamingGrepForSkipMarkerNotFlagged(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	stats := newRunStats("fix the login bug")
	stats.CommandsRun = []string{
		`grep -rn "t.Skip(" ./internal/`,
		"rg '@pytest.mark.skip' tests/",
		`git grep "xit(" -- src/`,
	}
	if msg := ag.checkSpecGaming(stats, "fix the login bug"); msg != "" {
		t.Fatalf("investigative grep for skip markers must not be flagged, got: %s", msg)
	}
}

func TestIssue544SpecGamingRealSkipIntroductionStillFlagged(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	stats := newRunStats("fix the flaky retry test")
	// sed that ACTUALLY inserts a skip marker — not investigative.
	stats.CommandsRun = []string{
		`sed -i 's/func TestFoo(t \*testing.T) {/func TestFoo(t *testing.T) { t.Skip("flaky")/' foo_test.go`,
	}
	if msg := ag.checkSpecGaming(stats, "fix the flaky retry test"); msg == "" {
		t.Fatal("introducing a t.Skip marker via sed must still be flagged")
	}
}

func TestIssue544SpecGamingTestWritingTaskExempt(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	// Task IS writing tests: only test files edited — expected, not gaming.
	stats := newRunStats("add unit tests for the parser")
	stats.FilesEdited = []string{"internal/parser/parse_test.go"}
	if msg := ag.checkSpecGaming(stats, "add unit tests for the parser"); msg != "" {
		t.Fatalf("test-writing task must be exempt from Pattern 1, got: %s", msg)
	}

	// Same shape but task is a bug fix: Pattern 1 must fire.
	ag2 := NewAgent(nil, nil, "sys", 5)
	defer ag2.Close()
	stats2 := newRunStats("fix the nil-pointer crash in the parser")
	stats2.FilesEdited = []string{"internal/parser/parse_test.go"}
	if msg := ag2.checkSpecGaming(stats2, stats2.UserPrompt); msg == "" {
		t.Fatal("non-test task editing only test files must still be flagged")
	}

	// CJK keyword (回归) also exempts.
	ag3 := NewAgent(nil, nil, "sys", 5)
	defer ag3.Close()
	stats3 := newRunStats("补充回归测试")
	stats3.FilesEdited = []string{"internal/parser/parse_test.go"}
	if msg := ag3.checkSpecGaming(stats3, stats3.UserPrompt); msg != "" {
		t.Fatalf("CJK test-writing task must be exempt, got: %s", msg)
	}
}

func TestIssue544SpecGamingTestWordWholeMatch(t *testing.T) {
	// "latest" contains "test" as substring but is NOT a test task.
	if isTestWritingTask("update to the latest version of the library") {
		t.Fatal(`"latest" must not count as the word "test"`)
	}
	if !isTestWritingTask("please write tests for the auth module") {
		t.Fatal(`"tests" (whole word) must count as a test-writing task`)
	}
	if !isTestWritingTask("补充回归测试") {
		t.Fatal("CJK 回归 must count as a test-writing task")
	}
}

// ---------- #544: wt_invalidation ----------

func TestIssue544WTInvalidationStashListReadOnly(t *testing.T) {
	w := newWTInvalidationState()
	w.recordRead("a.go")
	w.recordRead("b.go")

	// The #544 scenario: git_stash (a mutating-classified tool) invoked with
	// a READ-ONLY action — `git stash list`. Previously the args were
	// discarded entirely, so even list/show invalidated all cached reads.
	if msg := w.checkMutation("git_stash", `{"action":"list","description":"list stashes"}`); msg != "" {
		t.Fatalf("git_stash list is read-only, must not invalidate, got: %s", msg)
	}
	if msg := w.checkMutation("git_stash", `{"action":"show","description":"show stash@{0}"}`); msg != "" {
		t.Fatalf("git_stash show is read-only, must not invalidate, got: %s", msg)
	}
	// No `action` field, command-string form: `git stash list` embedded in a
	// mutating-class wrapper is still recognized as read-only.
	if msg := w.checkMutation("git_stash", `{"description":"list","command":"git stash list"}`); msg != "" {
		t.Fatalf("embedded `git stash list` must be read-only, got: %s", msg)
	}
	// The tracked reads must still be present (no silent clearing).
	if len(w.readFiles) != 2 {
		t.Fatalf("read tracking must be untouched by read-only calls, got %d files", len(w.readFiles))
	}
}

func TestIssue544WTInvalidationMutationsStillWarn(t *testing.T) {
	// git_stash default action (absent = push) mutates.
	w := newWTInvalidationState()
	w.recordRead("a.go")
	w.recordRead("b.go")
	if msg := w.checkMutation("git_stash", `{"description":"stash work"}`); msg == "" {
		t.Fatal("git_stash with default action (push) must still warn")
	}

	// git branch -D (mutating) inside a command string: the branch guard in
	// isReadOnlyGitInvocation must refuse read-only classification.
	w2 := newWTInvalidationState()
	w2.recordRead("a.go")
	w2.recordRead("b.go")
	if msg := w2.checkMutation("git_stash", `{"description":"d","command":"git branch -D feature"}`); msg == "" {
		t.Fatal("git branch -D must not be classified read-only")
	}

	// Explicit stash pop mutates.
	w3 := newWTInvalidationState()
	w3.recordRead("a.go")
	w3.recordRead("b.go")
	if msg := w3.checkMutation("git_stash", `{"action":"pop","description":"pop stash"}`); msg == "" {
		t.Fatal("git stash pop must still warn")
	}

	// Description mentioning "list" must NOT trick classification when the
	// real action mutates.
	w4 := newWTInvalidationState()
	w4.recordRead("a.go")
	w4.recordRead("b.go")
	if msg := w4.checkMutation("git_stash", `{"action":"drop","description":"list then drop"}`); msg == "" {
		t.Fatal("action=drop with list-flavored description must still warn")
	}
}

// ---------- #544: debug_stmt ----------

func TestIssue544DebugStmtCommentMentionNotFlagged(t *testing.T) {
	const oldContent = `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	// The ONLY change: a comment mentioning a removed debug call.
	const newContent = `package main

import "fmt"

func main() {
	// removed fmt.Println("dbg") below
	fmt.Println("hello")
}
`
	if msg := checkDebugStmts("main.go", oldContent, newContent); msg != "" {
		t.Fatalf("comment mention of fmt.Println must not be flagged, got: %s", msg)
	}
}

func TestIssue544DebugStmtRealIntroductionStillFlagged(t *testing.T) {
	const oldContent = `package main

func main() {
}
`
	const newContent = `package main

import "fmt"

func main() {
	fmt.Println("dbg: reached here")
}
`
	if msg := checkDebugStmts("main.go", oldContent, newContent); msg == "" {
		t.Fatal("real newly-introduced fmt.Println must still be flagged")
	} else if !strings.Contains(msg, "fmt.Print") {
		t.Fatalf("warning should name fmt.Print, got: %s", msg)
	}
}

func TestIssue544DebugStmtBlockCommentMentionNotFlagged(t *testing.T) {
	const oldContent = `function f() {}
`
	const newContent = `// header comment
function f() {}
/* debug note: console.log("x") was removed here */
`
	if msg := checkDebugStmts("app.js", oldContent, newContent); msg != "" {
		t.Fatalf("block-comment mention of console.log must not be flagged, got: %s", msg)
	}
}
