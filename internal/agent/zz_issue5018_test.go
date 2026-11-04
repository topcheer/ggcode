package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// newIssue5018Repo creates a git repo with tracked files:
//
//	internal/foo/a.go   (agent A's territory)
//	internal/bar/b.go   (agent B's territory)
func newIssue5018Repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	writeIssue5018File(t, dir, "internal/foo/a.go", "package foo\n\nfunc A() {}\n")
	writeIssue5018File(t, dir, "internal/bar/b.go", "package bar\n\nfunc B() {}\n")
	run("add", "-A")
	run("commit", "-m", "seed")
	return dir
}

func writeIssue5018File(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// capturingProvider wraps mockProvider and records Chat payloads so tests can
// assert on what the adversarial evaluator actually received.
type capturingProvider struct {
	inner    *mockProvider
	chatMsgs [][]provider.Message
}

func (c *capturingProvider) Name() string { return c.inner.Name() }

func (c *capturingProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	c.chatMsgs = append(c.chatMsgs, append([]provider.Message(nil), messages...))
	return c.inner.Chat(ctx, messages, tools)
}

func (c *capturingProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return c.inner.ChatStream(ctx, messages, tools)
}

func (c *capturingProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return c.inner.CountTokens(ctx, messages)
}

// TestIssue5018ScopedDiffExcludesForeignChanges verifies the core fix: on a
// shared worktree, a diff scoped by this run's FilesEdited must not include
// another agent's in-flight edits, while still containing the agent's own.
func TestIssue5018ScopedDiffExcludesForeignChanges(t *testing.T) {
	dir := newIssue5018Repo(t)

	// Agent A's half-finished edit (foreign, in-flight).
	writeIssue5018File(t, dir, "internal/foo/a.go", "package foo\n\nfunc A() { panic(\"wip\") }\n")
	// Agent B's edit (this run).
	writeIssue5018File(t, dir, "internal/bar/b.go", "package bar\n\nfunc B() int { return 42 }\n")

	a := &Agent{workingDir: dir}
	rs := &RunStats{FilesEdited: []string{"internal/bar/b.go"}}
	diff := a.collectAdversarialDiff(adversarialDiffPaths(a.WorkingDir(), rs)...)
	if diff == "" {
		t.Fatal("scoped diff must not be empty when the agent's own tracked file has uncommitted changes")
	}
	if strings.Contains(diff, "internal/foo/a.go") {
		t.Fatalf("scoped diff must not include foreign in-flight edits; diff:\n%s", diff)
	}
	if !strings.Contains(diff, "internal/bar/b.go") {
		t.Fatalf("scoped diff must include the agent's own edit; diff:\n%s", diff)
	}
}

// TestIssue5018ScopedDiffUntrackedFiltering verifies the untracked-file
// listing is scoped to the agent's own files, including the "??" collapsed
// directory form and prefix matching for per-file entries.
func TestIssue5018ScopedDiffUntrackedFiltering(t *testing.T) {
	dir := newIssue5018Repo(t)

	// Foreign untracked file (git lists "?? internal/foo/" once a single new
	// file exists in a previously contentless directory).
	writeIssue5018File(t, dir, "internal/foo/new_foreign.go", "package foo\n")
	// Own untracked file.
	writeIssue5018File(t, dir, "internal/bar/new_own.go", "package bar\n")

	a := &Agent{workingDir: dir}
	rs := &RunStats{FilesEdited: []string{filepath.Join(dir, "internal/bar/new_own.go")}}
	diff := a.collectAdversarialDiff(adversarialDiffPaths(a.WorkingDir(), rs)...)
	if strings.Contains(diff, "internal/foo") {
		t.Fatalf("untracked listing must exclude foreign untracked files; diff:\n%s", diff)
	}
	if !strings.Contains(diff, "internal/bar/new_own.go") {
		t.Fatalf("untracked listing must include own untracked file; diff:\n%s", diff)
	}
}

// TestIssue5018EmptyScopeFallsBackToFullDiff verifies behavior parity for the
// run_command-mediated edit case: no recorded FilesEdited -> full worktree
// diff (previous behavior), never an empty review.
func TestIssue5018EmptyScopeFallsBackToFullDiff(t *testing.T) {
	dir := newIssue5018Repo(t)
	writeIssue5018File(t, dir, "internal/foo/a.go", "package foo\n\nfunc A() { panic(\"wip\") }\n")
	writeIssue5018File(t, dir, "internal/bar/b.go", "package bar\n\nfunc B() int { return 42 }\n")

	a := &Agent{workingDir: dir}
	if paths := adversarialDiffPaths(a.WorkingDir(), &RunStats{}); len(paths) != 0 {
		t.Fatalf("empty FilesEdited must produce empty scope, got %v", paths)
	}
	diff := a.collectAdversarialDiff() // no paths -> full diff
	if !strings.Contains(diff, "internal/foo/a.go") || !strings.Contains(diff, "internal/bar/b.go") {
		t.Fatalf("unscoped fallback must return full worktree diff; diff:\n%s", diff)
	}
}

// TestIssue5018GateReviewScopePerAgent verifies the gate passes this run's
// FilesEdited into the diff collection: with a provider fake returning FAIL,
// the injected message must describe the per-file scoped review (not a
// whole-worktree "your diff" attribution).
func TestIssue5018GateReviewScopePerAgent(t *testing.T) {
	dir := newIssue5018Repo(t)

	// Foreign edit by agent A.
	writeIssue5018File(t, dir, "internal/foo/a.go", "package foo\n\nfunc A() { panic(\"wip\") }\n")
	// Own edit by agent B.
	writeIssue5018File(t, dir, "internal/bar/b.go", "package bar\n\nfunc B() int { return 1 }\n")

	mp := &capturingProvider{inner: &mockProvider{chatResp: &provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "VERDICT: FAIL\n- internal/bar/b.go:2: returns 1, task asked for 2"}}},
	}}}
	a := NewAgent(mp, nil, "", 1)
	a.workingDir = dir
	a.SetAdversarialReview(true)

	rs := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"internal/bar/b.go"},
	}
	msg := a.checkAdversarialReviewGate(context.Background(), rs, "implement B")
	if msg == "" {
		t.Fatal("gate must inject findings when the fake evaluator FAILs the agent's own change")
	}
	if !strings.Contains(msg, "files you edited in this run") {
		t.Fatalf("scoped message must disclose the per-run file scope; got:\n%s", msg)
	}
	if strings.Contains(msg, "shared worktree") {
		t.Fatalf("scoped message must not claim full-worktree scope; got:\n%s", msg)
	}

	// The scoped diff itself must not have carried the foreign file into the
	// evaluation payload.
	captured := mp.chatMsgs
	if len(captured) == 0 {
		t.Fatal("fake provider must have captured evaluator messages")
	}
	last := captured[len(captured)-1]
	var payload string
	for _, blk := range last[len(last)-1].Content {
		payload += blk.Text
	}
	if strings.Contains(payload, "internal/foo/a.go") {
		t.Fatalf("evaluator payload must not contain the foreign file; payload tail:\n%s", payload[strings.LastIndex(payload, "## Diff"):])
	}
	if !strings.Contains(payload, "internal/bar/b.go") {
		t.Fatalf("evaluator payload must contain the agent's own file; payload tail:\n%s", payload[strings.LastIndex(payload, "## Diff"):])
	}
}

// TestIssue5018FallbackMessageDisclosesScope verifies the residual-risk
// mitigation: when the fallback reviews the full worktree diff, the wording
// no longer claims the findings are the agent's own diff.
func TestIssue5018FallbackMessageDisclosesScope(t *testing.T) {
	msg := adversarialReviewFindingsMessage("- finding", false, 1)
	if !strings.Contains(msg, "full uncommitted worktree diff") {
		t.Fatalf("fallback message must disclose full-worktree scope; got:\n%s", msg)
	}
	if !strings.Contains(msg, "other agents") {
		t.Fatalf("fallback message must warn about other agents' in-flight edits; got:\n%s", msg)
	}
	if strings.Contains(msg, "files you edited in this run") {
		t.Fatalf("fallback message must not use the per-file scope wording; got:\n%s", msg)
	}
}

// TestIssue5018PathNormalization verifies absolute, relative, and duplicate
// paths normalize to a deduped repo-relative scope set.
func TestIssue5018PathNormalization(t *testing.T) {
	dir := t.TempDir()
	paths := adversarialDiffPaths(dir, &RunStats{FilesEdited: []string{
		filepath.Join(dir, "internal/bar/b.go"),
		"internal/bar/b.go",                    // duplicate after normalization
		"internal/../internal/bar/b.go",        // cleaned duplicate
		"./",                                   // working dir itself: must be dropped
		filepath.Join(dir, "..", "outside.go"), // outside working dir: kept absolute
	}})
	var relCount int
	for _, p := range paths {
		if p == "internal/bar/b.go" {
			relCount++
		}
	}
	if relCount != 1 {
		t.Fatalf("expected exactly one normalized internal/bar/b.go entry, got %v", paths)
	}
	if len(paths) != 2 {
		t.Fatalf("expected exactly 2 entries (deduped rel + outside abs), got %v", paths)
	}
}
