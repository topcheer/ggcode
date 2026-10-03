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
// The "dirty-trial" sentinel skips the commit (#2795 shape: verify-passed
// work left uncommitted in the worktree).
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
	if strings.Contains(prompt, "dirty-trial") {
		return nil
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

func TestTrialForkKeepsVerifyPassedUncommittedWinner(t *testing.T) {
	root := initTrialRepo(t)
	tl := newTrialTool(root, func(provider.TokenUsage) {})

	// One uncommitted-but-verify-passed trial vs one hard failure: the dirty
	// trial must win on VerifyPass (100 pts) and be kept despite Commits==0.
	res, err := tl.Execute(context.Background(), trialInput(t, TrialForkInput{
		Goal:           "add a marker file",
		Strategies:     []string{"dirty-trial skip-commit", "fail-now"},
		VerifyCmd:      "test -f TRIAL_MARKER",
		TimeoutSeconds: 120,
	}))
	if err != nil {
		t.Fatalf("system error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected usable result (verify passed), got error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "WINNER: trial 1") {
		t.Fatalf("verify-passed uncommitted trial should win:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "committed nothing") {
		t.Fatalf("expected empty-adopt warning for uncommitted winner:\n%s", res.Content)
	}

	// #2795: the winner worktree (with its uncommitted TRIAL_MARKER) must
	// survive cleanupWorktrees instead of being force-deleted.
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("worktree list: %v: %s", err, out)
	}
	if n := strings.Count(string(out), "worktree "); n != 2 { // main + kept winner
		t.Fatalf("expected 2 worktrees (main + kept winner), got %d:\n%s", n, out)
	}
	// git may report the /private-prefixed realpath on macOS; normalize
	// before comparing against root.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		p := strings.TrimPrefix(line, "worktree ")
		if p == root || p == realRoot {
			continue
		}
		if _, err := os.Stat(filepath.Join(p, "TRIAL_MARKER")); err != nil {
			t.Fatalf("winner work lost from %s: %v", p, err)
		}
		found = true
	}
	if !found {
		t.Fatalf("no trial worktree survived:\n%s", out)
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
	// #3224: with no usable trial (no commits, no verify pass) the report must
	// take the No-usable branch - no WINNER block, no kept-worktree reference,
	// no adopt hint pointing at worktrees cleanup already force-deleted.
	if !strings.Contains(res.Content, "No usable trial") {
		t.Fatalf("expected 'No usable trial' summary:\n%s", res.Content)
	}
	for _, banned := range []string{"WINNER", "kept worktree", "git apply"} {
		if strings.Contains(res.Content, banned) {
			t.Fatalf("all-failed report must not contain %q:\n%s", banned, res.Content)
		}
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

// TestPickWinnerUnusableOutranksUsable pins the #3224 score-inversion fact the
// Kept guard in formatTrialReport defends against: an UNUSABLE trial (no
// commits, no verify pass) can outscore a usable one - clean+completed+10
// files = 25 beats committed-but-timed-out-and-dirty = 20 - so a
// partially-usable run can still select an unkept winner whose worktree
// cleanupWorktrees already deleted.
func TestPickWinnerUnusableOutranksUsable(t *testing.T) {
	unusable := trialResult{Status: "completed", Commits: 0, Files: 10, Dirty: false}            // 25
	usable := trialResult{Status: "timeout", Commits: 1, Files: 0, Dirty: true, VerifyRun: true} // 20
	if scoreTrial(unusable) <= scoreTrial(usable) {
		t.Fatalf("test premise broken: unusable %d must outrank usable %d for the guard to matter", scoreTrial(unusable), scoreTrial(usable))
	}
	results := []trialResult{usable, unusable}
	if got := pickWinner(results); got != 1 {
		t.Fatalf("winner index = %d, want 1 (unusable outranks low-scoring usable)", got)
	}
}

// TestTrialReportUncommittedWarningOnlyForKept pins the #3224 Kept guard: the
// "passed verify but committed nothing" warning (which references a
// kept-worktree pointer and a git-apply hint) must only fire for a winner the
// keep gate actually kept. For an unkept 0-commit winner both the warning and
// the kept-worktree line must be absent - the worktree was force-deleted.
func TestTrialReportUncommittedWarningOnlyForKept(t *testing.T) {
	results := []trialResult{
		{Index: 1, Branch: "trial/a", Status: "timeout", Commits: 1, Dirty: true},
		{Index: 2, Branch: "trial/b", Status: "completed", Commits: 0, Files: 10},
	}
	// Unkept 0-commit winner: no kept-worktree pointer, no uncommitted warning.
	unkept := formatTrialReport("base1234", "", withIndexes(results), 1)
	for _, banned := range []string{"kept worktree", "committed nothing"} {
		if strings.Contains(unkept, banned) {
			t.Fatalf("unkept winner report must not contain %q:\n%s", banned, unkept)
		}
	}
	// Kept 0-commit winner (verify-passed, uncommitted worktree survives):
	// the warning fires and points at the real kept worktree.
	kept := results
	kept[1].Kept = true
	kept[1].Worktree = "/tmp/kept-wt"
	kept[1].VerifyRun, kept[1].VerifyPass = true, true
	out := formatTrialReport("base1234", "", withIndexes(kept), 1)
	if !strings.Contains(out, "committed nothing") {
		t.Fatalf("kept 0-commit winner must carry the uncommitted-work warning:\n%s", out)
	}
	if !strings.Contains(out, "/tmp/kept-wt") {
		t.Fatalf("kept winner must reference its surviving worktree:\n%s", out)
	}
}

// withIndexes renumbers Index fields to match slice positions, mirroring what
// Execute does for real runs.
func withIndexes(rs []trialResult) []trialResult {
	for i := range rs {
		rs[i].Index = i + 1
	}
	return rs
}
