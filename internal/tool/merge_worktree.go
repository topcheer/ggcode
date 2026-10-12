package tool

// merge_worktree: AI-mediated merge arbitration for parallel agent branches
// (Replit Agent 4 fork-per-task paradigm; sa-112 NEW_GAP).
//
// ggcode has the full parallel loop — worktree-isolated sub-agents
// (createAgentWorktree), durable boards, follow panels — but branch recovery
// ends with wait_agent.go literally telling the orchestrator "do NOT merge it
// yourself". conflict_detect.go detects markers but cannot drive a merge.
// This tool closes that last mile with a three-action protocol:
//
//	action=merge     git merge --no-commit of the worktree branch into the
//	                 parent tree. Clean → auto-finalized (merge commit).
//	                 Conflicting → the merge state is KEPT and a structured
//	                 arbitration report is returned: per-file conflict regions
//	                 (reusing DetectMergeConflicts) plus BOTH sides' intents
//	                 (recent commit subjects). The calling agent — the LLM —
//	                 performs the semantic resolution with edit_file, exactly
//	                 the Replit "AI-mediated conflict resolution" split.
//	action=finalize  after the agent resolved markers by hand: refuses while
//	                 unmerged files remain, optionally runs a verify gate
//	                 (build/test) and aborts back to clean on failure, then
//	                 creates the merge commit.
//	action=abort     git merge --abort — restores the parent tree; the
//	                 worktree branch is NEVER deleted (fail-closed, mirrors
//	                 exit_worktree's #1710 protections).
//
// Safety: the worktree must be anchored under <main-repo>/.ggcode/worktrees/
// (isInsideWorktree, #542 ownership rule); the parent tree must be clean
// before merge; nothing is pushed or branch-deleted here.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// MergeWorktree merges a ggcode-managed worktree branch back into the parent
// tree with structured conflict arbitration.
type MergeWorktree struct {
	WorkingDir string
}

func (t MergeWorktree) Name() string { return "merge_worktree" }

func (t MergeWorktree) Description() string {
	return "Merge a .ggcode/worktrees/ branch back into the parent tree with AI merge arbitration. " +
		"action=merge: clean merges auto-finalize; conflicts return a structured arbitration report " +
		"(per-file conflict regions + both sides' commit intents) while the merge state is kept — resolve the markers " +
		"with edit_file, then call action=finalize (optionally gated by verify_command). " +
		"action=abort restores the parent tree. The worktree branch is never deleted."
}

func (t MergeWorktree) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"worktree": {
				"type": "string",
				"description": "Worktree path (as returned by enter_worktree/spawn_agent) or its branch name. Must live under .ggcode/worktrees/."
			},
			"action": {
				"type": "string",
				"enum": ["merge", "finalize", "abort"],
				"description": "merge = start the merge (default); finalize = commit after you resolved the conflict markers; abort = roll the merge back."
			},
			"verify_command": {
				"type": "string",
				"description": "Optional command run in the parent tree before finalize commits; non-zero exit aborts the merge (fail-closed)."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
			}
		},
		"required": ["worktree", "description"]
	}`)
}

// mergeGitEnv builds the environment for git calls. When the host has no
// configured identity (fresh CI containers), merge/commit operations fail
// with "Committer identity unknown" — fall back to a tool-scoped identity
// (env vars, like -c, never touch the repo's own config).
func mergeGitEnv() []string {
	env := append(os.Environ(), "GIT_PAGER=cat")
	probe := exec.Command("git", "config", "--get", "user.email")
	probe.Env = env
	if err := probe.Run(); err != nil {
		env = append(env,
			"GIT_AUTHOR_NAME=ggcode", "GIT_AUTHOR_EMAIL=noreply@ggcode.dev",
			"GIT_COMMITTER_NAME=ggcode", "GIT_COMMITTER_EMAIL=noreply@ggcode.dev")
	}
	return env
}

// mergeGit runs git in dir and returns trimmed combined output.
func mergeGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (t MergeWorktree) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Worktree      string `json:"worktree"`
		Action        string `json:"action"`
		VerifyCommand string `json:"verify_command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.Worktree == "" {
		return Result{IsError: true, Content: "worktree is required"}, nil
	}
	action := args.Action
	if action == "" {
		action = "merge"
	}
	switch action {
	case "merge", "finalize", "abort":
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid action %q: must be merge, finalize, or abort", action)}, nil
	}

	gitRoot, err := findGitRoot(ctx, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("not a git repository: %v", err)}, nil
	}
	env := mergeGitEnv()

	// Resolve the worktree: accept an absolute path under .ggcode/worktrees/
	// or a bare name/branch (joined against the anchor directory).
	wtPath := args.Worktree
	if !filepath.IsAbs(wtPath) {
		wtPath = filepath.Join(gitRoot, ".ggcode", "worktrees", wtPath)
	}
	absWt, err := filepath.Abs(wtPath)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("cannot resolve worktree path: %v", err)}, nil
	}
	inside, _, err := isInsideWorktree(absWt)
	if err != nil || !inside {
		return Result{IsError: true, Content: fmt.Sprintf("%s is not a ggcode-managed worktree under .ggcode/worktrees/ (ownership check failed); refusing to merge", args.Worktree)}, nil
	}
	branch, err := mergeGit(ctx, absWt, env, "branch", "--show-current")
	if err != nil || branch == "" {
		return Result{IsError: true, Content: fmt.Sprintf("cannot determine branch of worktree %s (detached HEAD?)", absWt)}, nil
	}

	switch action {
	case "abort":
		return t.doAbort(ctx, env, gitRoot, branch)
	case "finalize":
		return t.doFinalize(ctx, env, gitRoot, branch, args.VerifyCommand)
	default:
		return t.doMerge(ctx, env, gitRoot, absWt, branch)
	}
}

// doMerge starts a --no-commit merge. Clean → finalize immediately; conflicting
// → keep the merge state and return the arbitration report.
func (t MergeWorktree) doMerge(ctx context.Context, env []string, gitRoot, absWt, branch string) (Result, error) {
	// Parent tree must be clean: a merge interleaved with in-flight edits
	// entangles two lines of work (same reasoning as #542 ownership).
	if st, err := mergeGit(ctx, gitRoot, env, "status", "--porcelain"); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git status failed: %s", st)}, nil
	} else if st != "" {
		return Result{IsError: true, Content: fmt.Sprintf("parent tree at %s is dirty; commit or stash before merging (refusing to entangle the merge with in-flight edits)", gitRoot)}, nil
	}

	if out, err := mergeGit(ctx, gitRoot, env, "merge", branch, "--no-edit", "--no-commit"); err != nil {
		// Conflict (or failure). Distinguish: a real conflict leaves unmerged
		// files; anything else is rolled back immediately.
		unmerged := unmergedFiles(ctx, env, gitRoot)
		if len(unmerged) == 0 {
			mergeGit(ctx, gitRoot, env, "merge", "--abort") // fail-closed cleanup
			return Result{IsError: true, Content: fmt.Sprintf("merge failed without conflicts (rolled back): %s", out)}, nil
		}
		report := t.arbitrationReport(ctx, env, gitRoot, branch, unmerged)
		return Result{Content: report}, nil
	}

	// Clean merge staged. Verify + commit via the shared finalize path
	// (verify_command empty here — clean merges are usually safe, and the
	// orchestrator can still run its own checks afterwards).
	return t.doFinalize(ctx, env, gitRoot, branch, "")
}

// unmergedFiles lists files with conflicts in the parent tree.
func unmergedFiles(ctx context.Context, env []string, gitRoot string) []string {
	out, err := mergeGit(ctx, gitRoot, env, "diff", "--name-only", "--diff-filter=U")
	if err != nil || out == "" {
		return nil
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files
}

// arbitrationReport assembles the structured material the calling agent needs
// for semantic resolution: per-file conflict regions and both sides' intents.
func (t MergeWorktree) arbitrationReport(ctx context.Context, env []string, gitRoot, branch string, files []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Merge of %s into %s hit %d conflicting file(s). The merge state is KEPT — resolve the markers with edit_file, then call merge_worktree with action=finalize (or action=abort to roll back; the branch is never deleted).\n\n",
		branch, parentLabel(ctx, env, gitRoot), len(files),
	))

	// Both sides' intents: recent commit subjects each side brings to the
	// merge point. This is what makes the resolution *semantic* rather than
	// textual — the agent reads WHY each side changed the region.
	ours := commitSubjects(ctx, env, gitRoot, fmt.Sprintf("%s..HEAD", branch))
	theirs := commitSubjects(ctx, env, gitRoot, fmt.Sprintf("HEAD..%s", branch))
	sb.WriteString(fmt.Sprintf("Parent side (ours) intents:\n%s\nBranch side (theirs, %s) intents:\n%s\n\n",
		intentLines(ours, "HEAD"), branch, intentLines(theirs, branch)))

	const maxReportFiles = 5
	for i, f := range files {
		if i >= maxReportFiles {
			sb.WriteString(fmt.Sprintf("... and %d more conflicting file(s)\n", len(files)-maxReportFiles))
			break
		}
		content, err := os.ReadFile(filepath.Join(gitRoot, f))
		if err != nil {
			sb.WriteString(fmt.Sprintf("- %s: unreadable (%v)\n", f, err))
			continue
		}
		regions := DetectMergeConflicts(string(content))
		sb.WriteString(fmt.Sprintf("- %s: %d conflict region(s)\n", f, len(regions)))
		for _, r := range regions {
			end := r.EndLine
			if r.Unclosed || end == 0 {
				end = r.StartLine
			}
			sb.WriteString(fmt.Sprintf("    lines %d-%d  (%s vs %s)%s\n",
				r.StartLine, end, conflictLabel(r.Branch1), conflictLabel(r.Branch2),
				boolSuffix(r.Unclosed, " [unclosed marker]")))
		}
	}
	sb.WriteString("\nNext: for each region above, decide which side (or which synthesis) matches the intents, edit the file to remove all markers, then merge_worktree action=finalize.")
	return sb.String()
}

// doFinalize verifies no unmerged files remain, optionally runs the verify
// gate, and creates the merge commit. Verify failure → abort (fail-closed).
func (t MergeWorktree) doFinalize(ctx context.Context, env []string, gitRoot, branch, verifyCommand string) (Result, error) {
	if left := unmergedFiles(ctx, env, gitRoot); len(left) > 0 {
		return Result{IsError: true, Content: fmt.Sprintf("cannot finalize: %d file(s) still have unresolved conflict markers (%s...); resolve them with edit_file first or use action=abort", len(left), strings.Join(left[:minInt(3, len(left))], ", "))}, nil
	}
	// A finalize without an in-progress merge would silently create an empty
	// state change; require MERGE_HEAD to exist.
	if _, err := mergeGit(ctx, gitRoot, env, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err != nil {
		return Result{IsError: true, Content: "no merge in progress in the parent tree; start with action=merge first"}, nil
	}

	if verifyCommand != "" {
		cmd := exec.CommandContext(ctx, "sh", "-c", verifyCommand)
		cmd.Dir = gitRoot
		cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
		if out, err := cmd.CombinedOutput(); err != nil {
			mergeGit(ctx, gitRoot, env, "merge", "--abort") // fail-closed: bad resolution must not land
			return Result{IsError: true, Content: fmt.Sprintf("verify_command failed (merge aborted, parent tree restored, branch %s intact): %s", branch, lastLines(string(out), 15))}, nil
		}
	}

	if out, err := mergeGit(ctx, gitRoot, env, "commit", "--no-edit"); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("merge commit failed (merge state kept; fix and re-run finalize, or abort): %s", out)}, nil
	}
	head, _ := mergeGit(ctx, gitRoot, env, "log", "-1", "--oneline")
	return Result{Content: fmt.Sprintf("Merged %s into the parent tree. %s The worktree branch is left intact; delete it only when you no longer need it.", branch, head)}, nil
}

// doAbort rolls the merge back. The worktree and its branch survive.
func (t MergeWorktree) doAbort(ctx context.Context, env []string, gitRoot, branch string) (Result, error) {
	if out, err := mergeGit(ctx, gitRoot, env, "merge", "--abort"); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git merge --abort failed: %s", out)}, nil
	}
	return Result{Content: fmt.Sprintf("Merge of %s aborted; parent tree restored. Worktree branch %s is intact (never deleted by this tool).", branch, branch)}, nil
}

// parentLabel names the merge target for reports ("HEAD (main)").
func parentLabel(ctx context.Context, env []string, gitRoot string) string {
	if b, err := mergeGit(ctx, gitRoot, env, "branch", "--show-current"); err == nil && b != "" {
		return "HEAD (" + b + ")"
	}
	return "HEAD"
}

// commitSubjects lists up to 5 one-line commit subjects in range.
func commitSubjects(ctx context.Context, env []string, gitRoot, revRange string) []string {
	out, err := mergeGit(ctx, gitRoot, env, "log", "--oneline", "-5", revRange)
	if err != nil || out == "" {
		return nil
	}
	var subs []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			subs = append(subs, l)
		}
	}
	return subs
}

func intentLines(subjects []string, side string) string {
	if len(subjects) == 0 {
		return "  (no unique commits on this side)"
	}
	var sb strings.Builder
	for _, s := range subjects {
		sb.WriteString("  - " + s + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func boolSuffix(b bool, s string) string {
	if b {
		return s
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// lastLines keeps the tail of verbose command output for error reports.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
