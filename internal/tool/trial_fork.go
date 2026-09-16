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
	"time"
	"unicode"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/subagent"
)

// TrialForkTool implements trajectory-level branching search ("fork and
// select"): instead of a single linear attempt at a task, it runs N parallel
// implementation trials, each in its own git worktree with a distinct strategy
// hint, verifies every result with a caller-supplied command, ranks the
// outcomes, and reports the winning branch. The main checkout is never
// modified; adopting the winner is an explicit follow-up step.
type TrialForkTool struct {
	Provider     provider.Provider
	Tools        *Registry
	AgentFactory subagent.AgentFactory
	WorkingDir   string
	OnUsage      func(provider.TokenUsage)
}

const (
	trialForkMinTrials     = 2
	trialForkMaxTrials     = 3
	trialForkDefaultTimout = 10 * time.Minute
	trialForkVerifyCap     = 5 * time.Minute
	trialForkSummaryMax    = 600
)

// trialForkBlockedTools are session-level tools a trial sub-agent must never
// use. Trials run inside an isolated worktree: they must not spawn further
// agents, touch worktrees, delegate externally, or mutate harness state.
var trialForkBlockedTools = []string{
	"spawn_agent", "wait_agent", "list_agents",
	"enter_worktree", "exit_worktree", "list_worktree",
	"trial_fork", "delegate", "restart", "switch_mode", "config", "im",
}

type TrialForkInput struct {
	Goal           string   `json:"goal"`
	Strategies     []string `json:"strategies"`
	VerifyCmd      string   `json:"verify_cmd"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type trialResult struct {
	Index      int
	Strategy   string
	Branch     string
	Worktree   string
	Status     string // completed | failed | timeout
	ErrMsg     string
	Summary    string
	Commits    int
	Files      int
	DiffStat   string
	Dirty      bool
	VerifyRun  bool
	VerifyPass bool
	VerifyOut  string
	Tokens     int
	Kept       bool // worktree dir kept for inspection (usable winner only)
}

func (t *TrialForkTool) Name() string { return "trial_fork" }

func (t *TrialForkTool) Description() string {
	return "Run 2-3 parallel, worktree-isolated implementation trials of one goal, each following a distinct strategy, then verify and rank them. Use when a task has several viable approaches or previous attempts keep failing and you want to try-and-select instead of iterating linearly. Each trial commits on its own trial/* branch; the main checkout is never modified. Optionally pass verify_cmd (shell command run inside each trial worktree, e.g. 'go build ./...') to decide the winner. The result reports per-trial status, diff stat, verification, and the recommended winning branch plus a non-destructive apply hint."
}

func (t *TrialForkTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "goal": {
      "type": "string",
      "description": "The implementation goal every trial must achieve (self-contained; trials share no context with this session)."
    },
    "strategies": {
      "type": "array",
      "items": {"type": "string"},
      "minItems": 2,
      "maxItems": 3,
      "description": "One distinct approach hint per trial (2-3 items). Make them genuinely different, e.g. minimal-patch vs. refactor vs. alternative API."
    },
    "verify_cmd": {
      "type": "string",
      "description": "Optional shell command executed inside each trial worktree after the trial finishes (e.g. 'go build ./... && go test ./...'). Trials passing verification outrank failing ones."
    },
    "timeout_seconds": {
      "type": "integer",
      "minimum": 60,
      "maximum": 1800,
      "description": "Per-trial wall-clock limit in seconds. Default 600."
    }
  },
  "required": ["goal", "strategies"]
}`)
}

func (t *TrialForkTool) Clone() Tool { cp := *t; return &cp }

func (t *TrialForkTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var in TrialForkInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Content: "invalid input: " + err.Error()}, nil
	}
	if err := t.validate(&in); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	if t.Provider == nil || t.Tools == nil || t.AgentFactory == nil {
		return Result{IsError: true, Content: "trial_fork requires an active LLM provider and tool registry, which are unavailable in this mode"}, nil
	}

	root, err := t.gitOut(t.WorkingDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Result{IsError: true, Content: "trial_fork must run inside a git repository: " + err.Error()}, nil
	}
	base, err := t.gitOut(root, "rev-parse", "HEAD")
	if err != nil {
		return Result{IsError: true, Content: "trial_fork needs at least one commit to fork from (HEAD is unborn): " + err.Error()}, nil
	}

	trialsDir := filepath.Join(root, ".ggcode", "trials")
	if err := os.MkdirAll(trialsDir, 0o755); err != nil {
		return Result{IsError: true, Content: "cannot create trials directory: " + err.Error()}, nil
	}

	timeout := trialForkDefaultTimout
	if in.TimeoutSeconds >= 60 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	uniq := fmt.Sprintf("%d", time.Now().UnixMilli()%1000000)

	// Create all worktrees first; on failure tear down the ones already made.
	branches := make([]string, len(in.Strategies))
	dirs := make([]string, len(in.Strategies))
	for i, strategy := range in.Strategies {
		slug := trialSlug(strategy, i+1, uniq)
		branch := "trial/" + slug
		dir := filepath.Join(trialsDir, slug)
		if _, err := t.gitOut(root, "worktree", "add", "-b", branch, dir, base); err != nil {
			t.cleanupWorktrees(root, dirs[:i])
			return Result{IsError: true, Content: fmt.Sprintf("failed to create worktree for trial %d: %v", i+1, err)}, nil
		}
		branches[i], dirs[i] = branch, dir
	}

	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: len(in.Strategies), Timeout: timeout})
	allToolInfo := make([]subagent.ToolInfo, 0, len(t.Tools.List()))
	for _, tl := range t.Tools.List() {
		allToolInfo = append(allToolInfo, tl)
	}

	results := make([]trialResult, len(in.Strategies))
	ids := make([]string, len(in.Strategies))
	for i, strategy := range in.Strategies {
		task := trialTaskPrompt(in.Goal, strategy, dirs[i])
		ids[i] = mgr.Spawn(fmt.Sprintf("trial-%d", i+1), task, fmt.Sprintf("trial %d/%d", i+1, len(in.Strategies)), nil, ctx)
		res := &results[i]
		res.Index, res.Strategy, res.Branch, res.Worktree = i+1, strategy, branches[i], dirs[i]
		cfg := subagent.RunnerConfig{
			Provider:     t.Provider,
			AllTools:     allToolInfo,
			Task:         task,
			Manager:      mgr,
			SubAgentID:   ids[i],
			AgentFactory: t.AgentFactory,
			WorkingDir:   dirs[i],
			OnUsage: func(u provider.TokenUsage) {
				res.Tokens += u.Total()
				if t.OnUsage != nil {
					t.OnUsage(u)
				}
			},
			BuildToolSet: func(_ []string, _ []subagent.ToolInfo) interface{} {
				cloned := t.Tools.Clone()
				for _, name := range cloned.ToolNames() {
					for _, blocked := range trialForkBlockedTools {
						if name == blocked {
							cloned.Unregister(name)
							break
						}
					}
				}
				return cloned
			},
		}
		tctx, cancel := context.WithTimeout(ctx, timeout)
		safego.Go("tool.trialfork.subagent", func() {
			defer cancel()
			subagent.Run(tctx, cfg)
		})
	}

	for i := range results {
		out, waitErr := subagent.Wait(ctx, mgr, ids[i])
		res := &results[i]
		res.Summary = trimSummary(out, trialForkSummaryMax)
		switch {
		case waitErr == nil:
			res.Status = "completed"
		case errors.Is(waitErr, context.DeadlineExceeded) || ctx.Err() != nil:
			res.Status = "timeout"
			res.ErrMsg = waitErr.Error()
		default:
			res.Status = "failed"
			res.ErrMsg = waitErr.Error()
		}
		t.inspectTrial(root, base, res)
		if strings.TrimSpace(in.VerifyCmd) != "" && res.Status == "completed" {
			v := runTrialVerify(ctx, res.Worktree, in.VerifyCmd, trialForkVerifyCap)
			res.VerifyRun, res.VerifyPass, res.VerifyOut = true, v.pass, v.output
		}
		t.progress(ctx, fmt.Sprintf("trial %d/%d: %s (verify %s)", res.Index, len(results), res.Status, trialVerifyLabel(*res)))
	}

	winner := pickWinner(results)
	keep := -1
	if winner >= 0 && results[winner].Commits > 0 {
		keep = winner
		results[winner].Kept = true
	}
	t.cleanupWorktrees(root, dirs, keep)

	anyUsable := false
	for _, r := range results {
		if r.Commits > 0 || r.VerifyPass {
			anyUsable = true
		}
	}
	report := formatTrialReport(base, in.VerifyCmd, results, winner)
	return Result{IsError: !anyUsable, Content: report}, nil
}

func (t *TrialForkTool) validate(in *TrialForkInput) error {
	in.Goal = strings.TrimSpace(in.Goal)
	if in.Goal == "" {
		return errors.New("trial_fork requires a non-empty 'goal'")
	}
	cleaned := make([]string, 0, len(in.Strategies))
	seen := map[string]bool{}
	for _, s := range in.Strategies {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		cleaned = append(cleaned, s)
	}
	if len(cleaned) < trialForkMinTrials || len(cleaned) > trialForkMaxTrials {
		return fmt.Errorf("trial_fork requires between %d and %d distinct non-empty strategies, got %d", trialForkMinTrials, trialForkMaxTrials, len(cleaned))
	}
	in.Strategies = cleaned
	return nil
}

// gitOut runs a git command in dir and returns trimmed stdout.
func (t *TrialForkTool) gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// inspectTrial fills in the git-level facts about a finished trial.
func (t *TrialForkTool) inspectTrial(root, base string, res *trialResult) {
	if out, err := t.gitOut(res.Worktree, "status", "--porcelain"); err == nil {
		res.Dirty = strings.TrimSpace(out) != ""
	}
	if out, err := t.gitOut(res.Worktree, "rev-list", "--count", base+"..HEAD"); err == nil {
		fmt.Sscanf(out, "%d", &res.Commits)
	}
	if names, err := t.gitOut(res.Worktree, "diff", "--name-only", base+"..HEAD"); err == nil && names != "" {
		res.Files = len(strings.Split(names, "\n"))
	}
	if stat, err := t.gitOut(res.Worktree, "diff", "--stat", base+"..HEAD"); err == nil {
		res.DiffStat = stat
	}
}

type trialVerifyOutput struct {
	pass   bool
	output string
}

func runTrialVerify(ctx context.Context, dir, verifyCmd string, cap time.Duration) trialVerifyOutput {
	vctx, cancel := context.WithTimeout(ctx, cap)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(vctx, "cmd", "/c", verifyCmd)
	} else {
		cmd = exec.CommandContext(vctx, "sh", "-c", verifyCmd)
	}
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	res := trialVerifyOutput{pass: err == nil, output: trimSummary(string(out), 400)}
	if err != nil && res.output == "" {
		res.output = err.Error()
	}
	return res
}

// cleanupWorktrees removes every worktree dir except the winner's. Branches
// are always kept so no committed work is lost.
func (t *TrialForkTool) cleanupWorktrees(root string, dirs []string, keep ...int) {
	skip := map[int]bool{}
	for _, i := range keep {
		skip[i] = true
	}
	for i, dir := range dirs {
		if dir == "" || skip[i] {
			continue
		}
		_, _ = t.gitOut(root, "worktree", "remove", "--force", dir)
	}
}

func trialTaskPrompt(goal, strategy, worktree string) string {
	return fmt.Sprintf(`[ISOLATED TRIAL] You are one of several parallel implementation trials.
WORKTREE: %s

GOAL:
%s

STRATEGY (follow this distinct approach; do not switch to a different one):
%s

Rules:
- Work ONLY inside the working directory above; never touch other checkouts.
- Finish the goal end-to-end, then commit all changes on the current branch with a descriptive message.
- If you get stuck on this strategy, still commit whatever partial progress compiles.
- End with one short paragraph summarizing what you did and any caveats.`, worktree, goal, strategy)
}

// trialSlug builds a filesystem/branch-safe unique name from a strategy hint.
func trialSlug(strategy string, i int, uniq string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strategy) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '/':
			b.WriteByte('-')
		}
		if b.Len() >= 24 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "trial"
	}
	return fmt.Sprintf("%s-t%d-%s", slug, i, uniq)
}

// scoreTrial ranks a trial: verified work beats unverified, committed work
// beats dirty trees, more files indicate more complete attempts.
func scoreTrial(r trialResult) int {
	score := 0
	if r.VerifyRun && r.VerifyPass {
		score += 100
	}
	if r.Commits > 0 {
		score += 20
	}
	if !r.Dirty {
		score += 10
	}
	if r.Status == "completed" {
		score += 5
	}
	files := r.Files
	if files > 10 {
		files = 10
	}
	return score + files
}

// pickWinner returns the index of the best trial (ties keep the earlier one).
func pickWinner(results []trialResult) int {
	best := -1
	bestScore := -1
	for i := range results {
		if s := scoreTrial(results[i]); s > bestScore {
			best, bestScore = i, s
		}
	}
	return best
}

// progress pushes an intermediate update to the TUI when one is attached.
func (t *TrialForkTool) progress(ctx context.Context, msg string) {
	if fn, ok := ctx.Value(ToolProgressKey{}).(ToolProgressFunc); ok && fn != nil {
		fn("trial_fork", "trial_fork", msg)
	}
}

func trialVerifyLabel(r trialResult) string {
	switch {
	case !r.VerifyRun:
		return "n/a"
	case r.VerifyPass:
		return "PASS"
	default:
		return "FAIL"
	}
}

func trimSummary(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexAny(cut, " \n"); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

func formatTrialReport(base, verifyCmd string, results []trialResult, winner int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Trial fork complete: %d trials forked from %s\n", len(results), abbrevSHA(base))
	if verifyCmd != "" {
		fmt.Fprintf(&sb, "verify command: %s\n", verifyCmd)
	}
	for _, r := range results {
		marker := ""
		if r.Index-1 == winner {
			marker = " [WINNER]"
		}
		fmt.Fprintf(&sb, "\n--- Trial %d%s: %q ---\n", r.Index, marker, r.Strategy)
		fmt.Fprintf(&sb, "branch: %s\n", r.Branch)
		fmt.Fprintf(&sb, "status: %s | commits: %d | files: %d | verify: %s | tokens: %d\n",
			r.Status, r.Commits, r.Files, trialVerifyLabel(r), r.Tokens)
		if r.Dirty {
			sb.WriteString("note: uncommitted changes remain in the trial\n")
		}
		if r.ErrMsg != "" {
			fmt.Fprintf(&sb, "error: %s\n", trimSummary(r.ErrMsg, 200))
		}
		if r.DiffStat != "" {
			fmt.Fprintf(&sb, "diff:\n%s\n", r.DiffStat)
		}
		if r.VerifyOut != "" {
			fmt.Fprintf(&sb, "verify output: %s\n", r.VerifyOut)
		}
		if r.Summary != "" {
			fmt.Fprintf(&sb, "summary: %s\n", r.Summary)
		}
	}
	if winner < 0 {
		sb.WriteString("\nNo usable trial: every attempt failed to produce committed work.\n")
	} else {
		w := results[winner]
		fmt.Fprintf(&sb, "\nWINNER: trial %d (branch %s)\n", w.Index, w.Branch)
		if w.Kept {
			fmt.Fprintf(&sb, "winner worktree kept for inspection: %s\n", w.Worktree)
		}
		fmt.Fprintf(&sb, "adopt (non-destructive, from your checkout): git diff %s..%s | git apply\n",
			abbrevSHA(base), w.Branch)
	}
	sb.WriteString("Losing trial worktrees were removed; all trial/* branches are kept.\n")
	return sb.String()
}

func abbrevSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
