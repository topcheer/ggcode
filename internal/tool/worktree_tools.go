package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// EnterWorktree creates a new git worktree for isolated work.
type EnterWorktree struct {
	WorkingDir string
}

func (t EnterWorktree) Name() string { return "enter_worktree" }

func (t EnterWorktree) Description() string {
	return "Create an isolated git worktree under .ggcode/worktrees/ with a new branch from HEAD. " +
		"Use the returned path as working_dir for operations inside the worktree."
}

func (t EnterWorktree) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {
			"type": "string",
			"description": "Name for the worktree (used as directory and branch name). Defaults to a random name."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"description"
	]
}`)
}

func (t EnterWorktree) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	name := args.Name
	if name == "" {
		// Generate a random-ish name
		name = fmt.Sprintf("wt-%s-%04d", time.Now().Format("20060102"), rand.Intn(10000))
	}

	// Sanitize name: only allow safe characters
	for _, c := range name {
		if !isWorktreeNameChar(c) {
			return Result{IsError: true, Content: fmt.Sprintf("invalid worktree name %q: only letters, digits, dots, underscores, and dashes allowed", name)}, nil
		}
	}
	// #1710 case 5: "."/".." pass the character check (dot allowed) but are
	// path components - filepath.Join(worktreesDir, "..") escapes the
	// worktrees directory.
	if !isSafeWorktreeName(name) {
		return Result{IsError: true, Content: fmt.Sprintf("invalid worktree name %q: must not be a path component ('.', '..') or contain separators", name)}, nil
	}

	// Find git root
	gitRoot, err := findGitRoot(ctx, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("not a git repository: %v", err)}, nil
	}

	worktreesDir := filepath.Join(gitRoot, ".ggcode", "worktrees")
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error creating worktrees dir: %v", err)}, nil
	}

	worktreePath := filepath.Join(worktreesDir, name)
	branchName := name

	// Create worktree with new branch from HEAD
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branchName, worktreePath, "HEAD")
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// #4802 (S-34): a name collision used to surface git's bare
		// "fatal: a branch named '<name>' already exists" with no guidance.
		// The dead end pushed agents toward main-tree checkout operations
		// (the friction layer of the 2026-10-31 shared-tree violations).
		// Enrich the two collision classes with the existing branch's head,
		// whether a worktree already holds it, and the concrete next moves.
		return Result{IsError: true, Content: t.collisionGuidance(ctx, gitRoot, branchName, worktreePath, out)}, nil
	}

	return Result{
		Content:             fmt.Sprintf("Created worktree at %s (branch: %s). SuggestedWorkingDir is set to this path; use the returned path for subsequent file and command operations inside the worktree.", worktreePath, branchName),
		SuggestedWorkingDir: worktreePath,
	}, nil
}

// collisionGuidance turns a failed `git worktree add` into an actionable
// error message (#4802 / S-34). Two collision classes are enriched:
//
//  1. Branch collision - "a branch named '<name>' already exists": reports
//     the existing branch's head SHA and whether a worktree already holds
//     it, then gives the three safe next moves (reuse via
//     `git worktree add <path> <branch>` when free, pick another name, or
//     recreate from HEAD with `git worktree add -B <name> <path> HEAD`
//     after confirming the old branch is disposable). Never touches the
//     main tree's checkout.
//  2. Any other failure - the raw git output, unchanged.
func (t EnterWorktree) collisionGuidance(ctx context.Context, gitRoot, branchName, worktreePath string, out []byte) string {
	raw := strings.TrimSpace(string(out))
	if !strings.Contains(raw, "already exists") {
		return fmt.Sprintf("error creating worktree: %s", raw)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("error creating worktree %q: %s\n", branchName, raw))
	// git quotes branch names with SINGLE quotes ("a branch named 'x'") -
	// %q would emit double quotes and never match.
	branchCollision := strings.Contains(raw, "a branch named '"+branchName+"'") || strings.Contains(raw, fmt.Sprintf("branch '%s' already exists", branchName))
	if !branchCollision {
		// Path/directory collision (e.g. the worktrees dir entry exists).
		sb.WriteString(fmt.Sprintf("Next: remove or rename the existing entry at %s, or pick a different worktree name.", worktreePath))
		return sb.String()
	}
	// Branch collision: enrich with the existing head + holder status.
	headCmd := exec.CommandContext(ctx, "git", "rev-parse", "--short", branchName)
	headCmd.Dir = gitRoot
	headCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	headOut, headErr := headCmd.Output()
	sb.WriteString(fmt.Sprintf("\nBranch %q already exists", branchName))
	if headErr == nil {
		sb.WriteString(fmt.Sprintf(" (head: %s)", strings.TrimSpace(string(headOut))))
	}
	if wtPath := worktreeHoldingBranch(ctx, gitRoot, branchName); wtPath != "" {
		sb.WriteString(fmt.Sprintf(" and is already checked out in worktree %s - reuse that worktree instead of creating a new one", wtPath))
		return sb.String()
	}
	sb.WriteString(fmt.Sprintf(". Next (all safe, no main-tree checkout needed):\n"+
		"  1. Reuse the existing branch: git worktree add %s %s\n"+
		"  2. Pick a different worktree name and retry enter_worktree\n"+
		"  3. Recreate the branch from current HEAD (discards the old branch's unique commits - verify first): git worktree add -B %s %s HEAD",
		worktreePath, branchName, branchName, worktreePath))
	return sb.String()
}

// worktreeHoldingBranch returns the path of the worktree that has branchName
// checked out, or "" when the branch is free. Reads `git worktree list
// --porcelain` block by block: each block starts with a "worktree <path>"
// line and may carry a "branch refs/heads/<name>" line.
func worktreeHoldingBranch(ctx context.Context, gitRoot, branchName string) string {
	listCmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	listCmd.Dir = gitRoot
	listCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	listOut, err := listCmd.Output()
	if err != nil {
		return ""
	}
	want := "refs/heads/" + branchName
	wtPath := ""
	for _, line := range strings.Split(string(listOut), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			wtPath = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch ") && strings.TrimSpace(strings.TrimPrefix(line, "branch ")) == want && wtPath != "":
			return wtPath
		}
	}
	return ""
}

// createAgentWorktree creates an isolated git worktree for a spawned
// sub-agent under <gitRoot>/.ggcode/worktrees/, cut from HEAD on a dedicated
// branch. The sub-agent runs with its working directory set to the worktree
// so its file edits cannot collide with the parent's working tree.
// Worktrees are left in place after the run (never auto-removed); the parent
// finds the path via the spawn result and Snapshot.Worktree.
func createAgentWorktree(ctx context.Context, workingDir, agentID string) (string, string, error) {
	gitRoot, err := findGitRoot(ctx, workingDir)
	if err != nil {
		return "", "", fmt.Errorf("not a git repository: %w", err)
	}

	worktreesDir := filepath.Join(gitRoot, ".ggcode", "worktrees")
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return "", "", fmt.Errorf("error creating worktrees dir: %w", err)
	}

	// Sub-agent IDs restart at sa-1 in every process, so embed a timestamp
	// and random suffix to avoid colliding with worktrees left behind by
	// earlier sessions.
	name := fmt.Sprintf("agent-%s-%s-%04d", agentID, time.Now().Format("0102-150405"), rand.Intn(10000))
	for _, c := range name {
		if !isWorktreeNameChar(c) {
			return "", "", fmt.Errorf("invalid worktree name %q", name)
		}
	}
	// #1710 case 5: "."/".." pass the character check but are path components.
	if !isSafeWorktreeName(name) {
		return "", "", fmt.Errorf("invalid worktree name %q", name)
	}

	worktreePath := filepath.Join(worktreesDir, name)
	branchName := name

	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branchName, worktreePath, "HEAD")
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("git worktree add: %s", strings.TrimSpace(string(out)))
	}
	debug.Log("tool", "created isolation worktree %s (branch %s) for %s", worktreePath, branchName, agentID)
	return worktreePath, branchName, nil
}

// ExitWorktree exits and optionally removes a git worktree.
type ExitWorktree struct {
	WorkingDir string
}

func (t ExitWorktree) Name() string { return "exit_worktree" }

func (t ExitWorktree) Description() string {
	return "Exit and optionally remove a git worktree. " +
		"Use this when the user asks to exit a worktree session. " +
		"'keep' leaves the worktree directory and branch intact for later use. " +
		"'remove' deletes the worktree directory and its branch. " +
		"If there are uncommitted changes, you must set discard_changes=true or the removal will be rejected."
}

func (t ExitWorktree) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": [
				"keep",
				"remove"
			],
			"description": "'keep' leaves the worktree directory and branch intact. 'remove' deletes both."
		},
		"discard_changes": {
			"type": "boolean",
			"default": false,
			"description": "If true, discard uncommitted changes when removing. Required when there are uncommitted changes."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"action",
		"description"
	]
}`)
}

func (t ExitWorktree) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action         string `json:"action"`
		DiscardChanges bool   `json:"discard_changes"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	// #1406-A: 'keep' vs remove used to be decided by a single =="keep"
	// check - every OTHER value, including a typo ('exit', 'close' - both
	// beyond CoerceEnumValues' distance-2 correction) AND a MISSING action
	// (json.Unmarshal does not enforce required), fell through to REMOVE:
	// worktree deleted + 'git branch -D'. Missing parameter meant delete.
	// Fail closed now: only the two spelled-out values act, anything else
	// is a parameter error (with a near-miss suggestion when one exists).
	if args.Action != "keep" && args.Action != "remove" {
		msg := fmt.Sprintf("invalid action %q: must be 'keep' or 'remove'. Refusing to default to the destructive 'remove'.", args.Action)
		if s := suggestClosestEnum(args.Action, []json.RawMessage{json.RawMessage(`"keep"`), json.RawMessage(`"remove"`)}); s != "" {
			msg += fmt.Sprintf(" Did you mean %s?", s)
		}
		return Result{IsError: true, Content: msg}, nil
	}

	// Find git root
	gitRoot, err := findGitRoot(ctx, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("not a git repository: %v", err)}, nil
	}

	// Check if we're actually in a worktree
	isWorktree, worktreePath, err := isInsideWorktree(gitRoot)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error checking worktree status: %v", err)}, nil
	}
	if !isWorktree {
		return Result{IsError: true, Content: "not currently inside a worktree created by enter_worktree"}, nil
	}

	// Find the main repo root so we can suggest switching back
	mainRepoRoot, _ := findGitRootFromWorktree(worktreePath)

	if args.Action == "keep" {
		return Result{
			Content:             fmt.Sprintf("Worktree at %s kept intact.", worktreePath),
			SuggestedWorkingDir: mainRepoRoot,
		}, nil
	}

	// action == "remove"
	// Check for uncommitted changes
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.Output()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error checking git status: %v", err)}, nil
	}
	if len(strings.TrimSpace(string(out))) > 0 && !args.DiscardChanges {
		return Result{IsError: true, Content: "worktree has uncommitted changes. Set discard_changes=true to remove anyway, or commit/stash your changes first."}, nil
	}

	// Get branch name before removing
	branchCmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	branchCmd.Dir = gitRoot
	branchCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	branchOut, _ := branchCmd.Output()
	branchName := strings.TrimSpace(string(branchOut))

	// Remove the worktree. #1356: --force only when the caller explicitly
	// authorized discarding. Passing it unconditionally disabled git's own
	// dirty-refusal protection at all times, leaving only the status check
	// above - a real TOCTOU window in this multi-agent/parallel-tool
	// codebase, where a concurrent write landing between check and remove
	// was silently destroyed. Without --force, git checks-and-removes
	// atomically and its "contains modified or untracked files" error
	// covers untracked files too.
	forceArgs := []string{}
	if args.DiscardChanges {
		forceArgs = []string{"--force"}
	}
	rmCmd := exec.CommandContext(ctx, "git", append(append([]string{"worktree", "remove"}, forceArgs...), worktreePath)...)
	if mainRepoRoot != "" {
		rmCmd.Dir = mainRepoRoot
	} else {
		rmCmd.Dir = filepath.Dir(filepath.Dir(worktreePath))
	}
	rmCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	if rmOut, err := rmCmd.CombinedOutput(); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error removing worktree: %s", strings.TrimSpace(string(rmOut)))}, nil
	}

	// Optionally delete the branch
	if branchName != "" && branchName != "main" && branchName != "master" {
		// #1710 case 1: gate the force-delete on unmerged/unpushed commits.
		// The old code ran `git branch -D` unconditionally and swallowed the
		// error, so committed-but-unpushed work was destroyed with ZERO gate -
		// strictly weaker protection than the dirty-files gate above, for data
		// that is worth strictly more.
		unmergedCmd := exec.CommandContext(ctx, "git", "rev-list", "--count", branchName, "--not", "HEAD")
		unpushedCmd := exec.CommandContext(ctx, "git", "rev-list", "--count", branchName, "--not", "--remotes")
		unmergedCmd.Dir = mainRepoRoot
		unpushedCmd.Dir = mainRepoRoot
		unmergedCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
		unpushedCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
		unmergedOut, uerr1 := unmergedCmd.Output()
		unpushedOut, uerr2 := unpushedCmd.Output()
		unmerged, _ := strconv.Atoi(strings.TrimSpace(string(unmergedOut)))
		unpushed, _ := strconv.Atoi(strings.TrimSpace(string(unpushedOut)))
		// If the safety probes themselves fail, do NOT force-delete (fail
		// closed); keep the branch and tell the user.
		if uerr1 == nil && uerr2 == nil && (unmerged > 0 || unpushed > 0) && !args.DiscardChanges {
			return Result{
				Content:             fmt.Sprintf("worktree removed, but branch %s has %d unmerged and %d unpushed commit(s); branch kept. Set discard_changes=true to delete the branch too, or push/merge it first (recoverable via reflog for ~30 days after -D).", branchName, unmerged, unpushed),
				SuggestedWorkingDir: mainRepoRoot,
			}, nil
		}
		if uerr1 != nil || uerr2 != nil {
			debug.Log("tool.worktree", "branch safety probe failed for %s (unmerged err=%v unpushed err=%v); keeping branch", branchName, uerr1, uerr2)
		} else {
			delCmd := exec.CommandContext(ctx, "git", "branch", "-D", branchName)
			delCmd.Dir = mainRepoRoot
			delCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
			_ = delCmd.Run() // best effort
		}
	}

	return Result{
		Content:             fmt.Sprintf("Removed worktree %s", worktreePath),
		SuggestedWorkingDir: mainRepoRoot,
	}, nil
}

// isWorktreeNameChar returns true for characters allowed in worktree names.
func isWorktreeNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
}

// isSafeWorktreeName rejects names that are themselves path components
// (#1710 case 5): "." and ".." pass isWorktreeNameChar (dot allowed) and
// then escape the worktrees directory through filepath.Join.
func isSafeWorktreeName(name string) bool {
	if name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, string(os.PathSeparator)) {
		return false
	}
	return true
}

// findGitRoot finds the git repository root from a directory.
func findGitRoot(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository")
	}
	return strings.TrimSpace(string(out)), nil
}

// isInsideWorktree reports whether gitDir is the root of a worktree created
// by enter_worktree, returning the worktree path when true.
//
// Ownership verification (#542): every linked worktree — manual `git worktree
// add`, submodule, or ggcode-managed — has a regular .git FILE whose content
// is "gitdir: <main-repo>/.git/worktrees/<name>". That structure alone proves
// nothing about who created the worktree. The previous implementation treated
// ANY .git-file repository as a ggcode worktree, so exit_worktree(remove)
// destructively deleted user-created worktrees and force-deleted their
// unpushed branches. Now we additionally require that the worktree checkout
// path is anchored under <main-repo>/.ggcode/worktrees/ — the location where
// enter_worktree creates its worktrees.
func isInsideWorktree(gitDir string) (bool, string, error) {
	absPath, _ := filepath.Abs(gitDir)

	gitFile := filepath.Join(gitDir, ".git")
	info, err := os.Lstat(gitFile)
	if err != nil || !info.Mode().IsRegular() {
		// .git missing or a directory: main repository, not a linked worktree.
		return false, "", nil
	}
	data, err := os.ReadFile(gitFile)
	if err != nil || !strings.HasPrefix(string(data), "gitdir: ") {
		// Unreadable or malformed .git file: cannot verify ownership, refuse.
		return false, "", nil
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(gitDir, gitdir)
	}
	// gitdir points at <main-repo>/.git/worktrees/<name>; three levels up is
	// the main repository root (same layout findGitRootFromWorktree relies on).
	mainRoot := filepath.Dir(filepath.Dir(filepath.Dir(gitdir)))

	// Anchored containment: the worktree checkout must live under
	// <main-repo>/.ggcode/worktrees/ — not merely have that substring
	// somewhere along its path.
	worktreesDir := filepath.Join(mainRoot, ".ggcode", "worktrees")
	resolvedPath, resolvedBase := absPath, worktreesDir
	if r, err := filepath.EvalSymlinks(absPath); err == nil {
		resolvedPath = r
	}
	if r, err := filepath.EvalSymlinks(worktreesDir); err == nil {
		resolvedBase = r
	}
	rel, err := filepath.Rel(resolvedBase, resolvedPath)
	if err != nil || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, "", nil
	}
	return true, absPath, nil
}

// findGitRootFromWorktree finds the main repo root from a worktree path.
func findGitRootFromWorktree(worktreePath string) (string, error) {
	gitFile := filepath.Join(worktreePath, ".git")
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", err
	}
	// Content is like: gitdir: /path/to/main-repo/.git/worktrees/wt-name
	gitdir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
	// gitdir points to .git/worktrees/<name>, so we need 3 levels up:
	// .git/worktrees/<name> → .git/worktrees → .git → main repo root
	gitDir := filepath.Dir(filepath.Dir(filepath.Dir(gitdir)))
	return gitDir, nil
}

// ListWorktree lists all git worktrees with their branches, dirty status, and paths.
type ListWorktree struct {
	WorkingDir string
}

func (t ListWorktree) Name() string { return "list_worktree" }

func (t ListWorktree) Description() string {
	return "List all git worktrees with branch, dirty status, and last commit. " +
		"Use before creating a new worktree to avoid name collisions, or to find an existing worktree to resume work in."
}

func (t ListWorktree) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["description"]
	}`)
}

type worktreeEntry struct {
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	HEAD      string `json:"head"`
	Dirty     bool   `json:"dirty"`
	IsCurrent bool   `json:"is_current"`
}

func (t ListWorktree) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	gitRoot, err := findGitRoot(ctx, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("not a git repository: %v", err)}, nil
	}

	entries, err := parseWorktreeList(ctx, gitRoot)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error listing worktrees: %v", err)}, nil
	}

	for i := range entries {
		if entries[i].Path == gitRoot || (t.WorkingDir != "" && entries[i].Path == t.WorkingDir) {
			entries[i].IsCurrent = true
		}
	}

	checkWorktreeDirty(ctx, entries)

	return Result{
		Content: formatWorktreeList(entries),
	}, nil
}

// parseWorktreeList runs `git worktree list --porcelain` and parses the output.
func parseWorktreeList(ctx context.Context, gitRoot string) ([]worktreeEntry, error) {
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var entries []worktreeEntry
	var cur *worktreeEntry
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			wtPath := strings.TrimPrefix(line, "worktree ")
			entries = append(entries, worktreeEntry{Path: wtPath})
			cur = &entries[len(entries)-1]
		case cur != nil && strings.HasPrefix(line, "HEAD "):
			cur.HEAD = strings.TrimPrefix(line, "HEAD ")
		case cur != nil && strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	return entries, nil
}

// checkWorktreeDirty checks each worktree for uncommitted changes in-place.
func checkWorktreeDirty(ctx context.Context, entries []worktreeEntry) {
	for i := range entries {
		stCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
		stCmd.Dir = entries[i].Path
		stCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
		stOut, stErr := stCmd.Output()
		if stErr == nil && len(strings.TrimSpace(string(stOut))) > 0 {
			entries[i].Dirty = true
		}
		// #1710 case 5: a failed probe used to leave Dirty=false - a
		// worktree whose status could not be read rendered "clean". Fail
		// safe: mark dirty (unknown state must not look safe).
		if stErr != nil {
			debug.Log("tool.worktree", "dirty probe failed for %s: %v; marking dirty (fail-safe)", entries[i].Path, stErr)
			entries[i].Dirty = true
		}
	}
}

// formatWorktreeList renders worktree entries as a human-readable summary.
func formatWorktreeList(entries []worktreeEntry) string {
	if len(entries) == 0 {
		return "0 worktree(s)."
	}
	var sb strings.Builder
	for _, e := range entries {
		marker := " "
		if e.IsCurrent {
			marker = "*"
		}
		dirtyStr := ""
		if e.Dirty {
			dirtyStr = " (uncommitted changes)"
		}
		branchStr := e.Branch
		if branchStr == "" {
			if len(e.HEAD) > 7 {
				branchStr = "(detached HEAD " + e.HEAD[:7] + ")"
			} else {
				branchStr = "(detached HEAD " + e.HEAD + ")"
			}
		}
		sb.WriteString(fmt.Sprintf("%s %-40s  %s%s\n", marker, e.Path, branchStr, dirtyStr))
	}
	return fmt.Sprintf("%d worktree(s):\n%s", len(entries), strings.TrimSpace(sb.String()))
}

// Clone returns an independent copy of ListWorktree for use by a different agent.
func (t ListWorktree) Clone() Tool {
	return &ListWorktree{WorkingDir: t.WorkingDir}
}

// Clone returns an independent copy of EnterWorktree for use by a different agent.
func (t EnterWorktree) Clone() Tool {
	return &EnterWorktree{WorkingDir: t.WorkingDir}
}

// Clone returns an independent copy of ExitWorktree for use by a different agent.
func (t ExitWorktree) Clone() Tool {
	return &ExitWorktree{WorkingDir: t.WorkingDir}
}
