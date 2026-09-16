package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
)

func initTrialRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if runtime.GOOS == "windows" {
		t.Skip("trial fork integration tests require a POSIX shell")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "--no-verify", "-m", "initial")
	return dir
}

// trialStubRunner fakes a trial sub-agent: it writes a marker file and commits
// inside the worktree named in the prompt, unless the prompt contains the
// "fail-now" sentinel, in which case it errors like a stuck trial would.
type trialStubRunner struct{}

func (r *trialStubRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	if strings.Contains(prompt, "fail-now") {
		return errors.New("stub trial failed")
	}
	wt := trialWorktreeFromPrompt(prompt)
	if wt == "" {
		return errors.New("prompt has no WORKTREE line")
	}
	if err := os.WriteFile(filepath.Join(wt, "TRIAL_MARKER"), []byte("done\n"), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"-C", wt, "add", "-A"},
		{"-C", wt, "commit", "--no-verify", "-m", "trial work"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, out)
		}
	}
	return nil
}

func trialWorktreeFromPrompt(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "WORKTREE: ") {
			return strings.TrimPrefix(line, "WORKTREE: ")
		}
	}
	return ""
}

func trialStubFactory(provider.Provider, interface{}, string, int) subagent.AgentRunner {
	return &trialStubRunner{}
}

// trialStubProvider is a minimal provider.Provider; the factory stub never
// calls it, so every method is a no-op.
type trialStubProvider struct{}

func (trialStubProvider) Name() string              { return "stub" }
func (trialStubProvider) ReasoningEffort() string   { return "" }
func (trialStubProvider) SetReasoningEffort(string) {}
func (trialStubProvider) ToolChoice() string        { return "" }
func (trialStubProvider) SetToolChoice(string)      {}
func (trialStubProvider) CountTokens(_ context.Context, _ []provider.Message) (int, error) {
	return 0, nil
}
func (trialStubProvider) ChatStream(_ context.Context, _ []provider.Message, _ []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}
func (trialStubProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return nil, errors.New("not used in trial tests")
}

func newTrialTool(dir string, onUsage func(provider.TokenUsage)) *TrialForkTool {
	return &TrialForkTool{
		Provider:     &trialStubProvider{},
		Tools:        NewRegistry(),
		AgentFactory: trialStubFactory,
		WorkingDir:   dir,
		OnUsage:      onUsage,
	}
}

func trialInput(t *testing.T, in TrialForkInput) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTrialForkValidation(t *testing.T) {
	tl := newTrialTool(t.TempDir(), nil)
	cases := []struct {
		name    string
		input   TrialForkInput
		wantSub string
	}{
		{"empty goal", TrialForkInput{Strategies: []string{"a", "b"}}, "goal"},
		{"one strategy", TrialForkInput{Goal: "g", Strategies: []string{"a"}}, "between 2 and 3"},
		{"four strategies", TrialForkInput{Goal: "g", Strategies: []string{"a", "b", "c", "d"}}, "between 2 and 3"},
		{"duplicate strategies", TrialForkInput{Goal: "g", Strategies: []string{"same", " same "}}, "between 2 and 3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tl.Execute(context.Background(), trialInput(t, tc.input))
			if err != nil {
				t.Fatalf("system error: %v", err)
			}
			if !res.IsError || !strings.Contains(res.Content, tc.wantSub) {
				t.Fatalf("want error containing %q, got isError=%v content=%q", tc.wantSub, res.IsError, res.Content)
			}
		})
	}
}

func TestTrialForkMissingDeps(t *testing.T) {
	tl := newTrialTool(initTrialRepo(t), nil)
	tl.Provider = nil
	res, err := tl.Execute(context.Background(), trialInput(t, TrialForkInput{
		Goal: "g", Strategies: []string{"a", "b"},
	}))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "unavailable") {
		t.Fatalf("want unavailable-deps error, got isError=%v err=%v content=%q", res.IsError, err, res.Content)
	}
}

func TestTrialForkNotGitRepo(t *testing.T) {
	tl := newTrialTool(t.TempDir(), nil)
	res, err := tl.Execute(context.Background(), trialInput(t, TrialForkInput{
		Goal: "g", Strategies: []string{"a", "b"},
	}))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "git repository") {
		t.Fatalf("want git-repo error, got isError=%v err=%v content=%q", res.IsError, err, res.Content)
	}
}

func TestTrialForkUnbornRepo(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	tl := newTrialTool(dir, nil)
	res, err := tl.Execute(context.Background(), trialInput(t, TrialForkInput{
		Goal: "g", Strategies: []string{"a", "b"},
	}))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "unborn") {
		t.Fatalf("want unborn-HEAD error, got isError=%v err=%v content=%q", res.IsError, err, res.Content)
	}
}

func TestTrialForkRunSelectsWinner(t *testing.T) {
	root := initTrialRepo(t)
	// OnUsage is wired but never fires with the stub runner (no real LLM
	// traffic); it must simply not panic.
	tl := newTrialTool(root, func(provider.TokenUsage) {})

	res, err := tl.Execute(context.Background(), trialInput(t, TrialForkInput{
		Goal:           "add a marker file",
		Strategies:     []string{"direct approach", "fail-now"},
		VerifyCmd:      "test -f TRIAL_MARKER",
		TimeoutSeconds: 120,
	}))
	if err != nil {
		t.Fatalf("system error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "WINNER: trial 1") {
		t.Fatalf("trial 1 should win:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "verify: PASS") || !strings.Contains(res.Content, "verify: n/a") {
		t.Fatalf("expected PASS for winner and n/a for failed trial:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "git diff") || !strings.Contains(res.Content, "git apply") {
		t.Fatalf("expected non-destructive apply hint:\n%s", res.Content)
	}

	// Winner worktree kept, loser worktree removed, both branches intact.
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("worktree list: %v: %s", err, out)
	}
	if n := strings.Count(string(out), "worktree "); n != 2 { // main + winner
		t.Fatalf("expected 2 worktrees (main + winner), got %d:\n%s", n, out)
	}
	branches, err := exec.Command("git", "-C", root, "branch", "--format=%(refname:short)").CombinedOutput()
	if err != nil {
		t.Fatalf("branch list: %v: %s", err, branches)
	}
	if !strings.Contains(string(branches), "trial/direct-approach-t1") ||
		!strings.Contains(string(branches), "trial/fail-now-t2") {
		t.Fatalf("trial branches must be kept:\n%s", branches)
	}
}

func TestTrialForkRunAllFailed(t *testing.T) {
	root := initTrialRepo(t)
	tl := newTrialTool(root, nil)
	res, err := tl.Execute(context.Background(), trialInput(t, TrialForkInput{
		Goal:           "impossible goal",
		Strategies:     []string{"fail-now-1", "fail-now-2"},
		TimeoutSeconds: 120,
	}))
	if err != nil {
		t.Fatalf("system error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("all-failed trials should be an error result:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "status: failed") {
		t.Fatalf("expected failed statuses in report:\n%s", res.Content)
	}
	// No trial produced work: all worktrees must be cleaned up.
	out, _ := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").CombinedOutput()
	if n := strings.Count(string(out), "worktree "); n != 1 {
		t.Fatalf("expected only the main worktree to remain, got %d:\n%s", n, out)
	}
}

func TestTrialSlug(t *testing.T) {
	// Explicit checks:
	if got := trialSlug("Direct Approach!", 1, "42"); got != "direct-approach-t1-42" {
		t.Fatalf("slug = %q", got)
	}
	if got := trialSlug("  --__// ", 2, "42"); got != "trial-t2-42" {
		t.Fatalf("slug = %q", got)
	}
	if got := trialSlug("onesuperlongstrategywithmanycharsandmore", 3, "42"); got != "onesuperlongstrategywith-t3-42" {
		t.Fatalf("slug = %q", got)
	}
}

func TestScoreAndPickWinner(t *testing.T) {
	base := trialResult{Status: "completed", Commits: 1, Files: 3, VerifyRun: true, VerifyPass: true}
	if scoreTrial(base) <= scoreTrial(trialResult{Status: "failed"}) {
		t.Fatal("verified trial must outrank failed trial")
	}
	results := []trialResult{
		{Index: 1, Status: "completed", Commits: 1, Files: 2, VerifyRun: true, VerifyPass: false},
		{Index: 2, Status: "completed", Commits: 2, Files: 5, VerifyRun: true, VerifyPass: true},
		{Index: 3, Status: "failed"},
	}
	if got := pickWinner(results); got != 1 {
		t.Fatalf("winner index = %d, want 1", got)
	}
	// Ties keep the earlier trial.
	tie := []trialResult{{Index: 1, Status: "completed", Commits: 1}, {Index: 2, Status: "completed", Commits: 1}}
	if got := pickWinner(tie); got != 0 {
		t.Fatalf("tie winner index = %d, want 0", got)
	}
}

func TestTrimSummary(t *testing.T) {
	if got := trimSummary("short", 100); got != "short" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("word ", 400)
	got := trimSummary(long, 50)
	if len(got) > 52 || !strings.HasSuffix(got, "…") {
		t.Fatalf("trimSummary produced %q (len %d)", got, len(got))
	}
}
