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

func resolveWorktreeBase(ctx context.Context, gitRoot string) (base string, note string) {
	warn := func(ref string) string {
		return fmt.Sprintf("Warning: worktree is based on %s (%.7s), NOT origin/main - the remote was unreachable or has no main branch; it may carry unmerged commits (#4778).", ref, base)
	}

	fetch := exec.CommandContext(ctx, "git", "fetch", "origin", "main", "--quiet")
	fetch.Dir = gitRoot
	fetch.Env = append(os.Environ(), "GIT_PAGER=cat")
	_ = fetch.Run()
	for _, ref := range []string{"origin/main", "main", "master"} {
		c := exec.CommandContext(ctx, "git", "rev-parse", "--verify", ref+"^{commit}")
		c.Dir = gitRoot
		c.Env = append(os.Environ(), "GIT_PAGER=cat")
		out, err := c.Output()
		if err == nil {
			base = strings.TrimSpace(string(out))
			if ref == "origin/main" {
				return base, fmt.Sprintf("Base: origin/main @ %.7s.", base)
			}
			return base, warn(ref)
		}
	}

	c := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	c.Dir = gitRoot
	c.Env = append(os.Environ(), "GIT_PAGER=cat")
	if out, err := c.Output(); err == nil {
		base = strings.TrimSpace(string(out))
		return base, warn("HEAD")
	}
	return "HEAD", "Warning: could not resolve a base commit; using HEAD as-is."
}

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

		name = fmt.Sprintf("wt-%s-%04d", time.Now().Format("20060102"), rand.Intn(10000))
	}

	for _, c := range name {
		if !isWorktreeNameChar(c) {
			return Result{IsError: true, Content: fmt.Sprintf("invalid worktree name %q: only letters, digits, dots, underscores, and dashes allowed", name)}, nil
		}
	}

	if !isSafeWorktreeName(name) {
		return Result{IsError: true, Content: fmt.Sprintf("invalid worktree name %q: must not be a path component ('.', '..') or contain separators", name)}, nil
	}

	gitRoot, err := findGitRoot(ctx, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("not a git repository: %v", err)}, nil
	}

	// #5413: inside a worktree created by this tool, .git is a FILE
	// (gitdir pointer), so findGitRoot stops at the worktree itself and a
	// second enter_worktree nests .ggcode/worktrees one level deeper
	// (6-deep chains observed). Resolve back to the MAIN tree root so
	// sibling worktrees always land at <main>/.ggcode/worktrees/.
	if isWorktreeCheckout(gitRoot) {
		if main, merr := findGitRootFromWorktree(gitRoot); merr == nil {
			gitRoot = main
		}
	}

	worktreesDir := filepath.Join(gitRoot, ".ggcode", "worktrees")
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error creating worktrees dir: %v", err)}, nil
	}

	worktreePath := filepath.Join(worktreesDir, name)
	branchName := name

	base, baseNote := resolveWorktreeBase(ctx, gitRoot)
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branchName, worktreePath, base)
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.CombinedOutput()
	if err != nil {

		return Result{IsError: true, Content: t.collisionGuidance(ctx, gitRoot, branchName, worktreePath, out)}, nil
	}

	return Result{
		Content:             fmt.Sprintf("Created worktree at %s (branch: %s). %s SuggestedWorkingDir is set to this path; use the returned path for subsequent file and command operations inside the worktree.", worktreePath, branchName, baseNote+" "),
		SuggestedWorkingDir: worktreePath,
	}, nil
}

func (t EnterWorktree) collisionGuidance(ctx context.Context, gitRoot, branchName, worktreePath string, out []byte) string {
	raw := strings.TrimSpace(string(out))
	if !strings.Contains(raw, "already exists") {
		return fmt.Sprintf("error creating worktree: %s", raw)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("error creating worktree %q: %s\n", branchName, raw))

	branchCollision := strings.Contains(raw, "a branch named '"+branchName+"'") || strings.Contains(raw, fmt.Sprintf("branch '%s' already exists", branchName))
	if !branchCollision {

		sb.WriteString(fmt.Sprintf("Next: remove or rename the existing entry at %s, or pick a different worktree name.", worktreePath))
		return sb.String()
	}

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

func createAgentWorktree(ctx context.Context, workingDir, agentID string) (string, string, error) {
	gitRoot, err := findGitRoot(ctx, workingDir)
	if err != nil {
		return "", "", fmt.Errorf("not a git repository: %w", err)
	}

	worktreesDir := filepath.Join(gitRoot, ".ggcode", "worktrees")
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return "", "", fmt.Errorf("error creating worktrees dir: %w", err)
	}

	name := fmt.Sprintf("agent-%s-%s-%04d", agentID, time.Now().Format("0102-150405"), rand.Intn(10000))
	for _, c := range name {
		if !isWorktreeNameChar(c) {
			return "", "", fmt.Errorf("invalid worktree name %q", name)
		}
	}

	if !isSafeWorktreeName(name) {
		return "", "", fmt.Errorf("invalid worktree name %q", name)
	}

	worktreePath := filepath.Join(worktreesDir, name)
	branchName := name

	base, baseNote := resolveWorktreeBase(ctx, gitRoot)
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branchName, worktreePath, base)
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("git worktree add: %s", strings.TrimSpace(string(out)))
	}
	debug.Log("tool", "created isolation worktree %s (branch %s, base %s) for %s [%s]", worktreePath, branchName, base[:7], agentID, baseNote)
	return worktreePath, branchName, nil
}

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
			"description": "If true, discard uncommitted changes when removing. Required when there are uncommitted changes. Does NOT authorize deleting branches that carry unpushed/unmerged commits - that needs force_delete_branch."
		},
		"force_delete_branch": {
			"type": "boolean",
			"default": false,
			"description": "(#5212) Required to delete a branch that carries commits not on any remote or not merged into HEAD. discard_changes alone never deletes such a branch."
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
		Action            string `json:"action"`
		DiscardChanges    bool   `json:"discard_changes"`
		ForceDeleteBranch bool   `json:"force_delete_branch"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Action != "keep" && args.Action != "remove" {
		msg := fmt.Sprintf("invalid action %q: must be 'keep' or 'remove'. Refusing to default to the destructive 'remove'.", args.Action)
		if s := suggestClosestEnum(args.Action, []json.RawMessage{json.RawMessage(`"keep"`), json.RawMessage(`"remove"`)}); s != "" {
			msg += fmt.Sprintf(" Did you mean %s?", s)
		}
		return Result{IsError: true, Content: msg}, nil
	}

	gitRoot, err := findGitRoot(ctx, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("not a git repository: %v", err)}, nil
	}

	isWorktree, worktreePath, err := isInsideWorktree(gitRoot)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error checking worktree status: %v", err)}, nil
	}
	if !isWorktree {
		return Result{IsError: true, Content: "not currently inside a worktree created by enter_worktree"}, nil
	}

	mainRepoRoot, _ := findGitRootFromWorktree(worktreePath)

	if args.Action == "keep" {
		return Result{
			Content:             fmt.Sprintf("Worktree at %s kept intact.", worktreePath),
			SuggestedWorkingDir: mainRepoRoot,
		}, nil
	}

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

	branchCmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	branchCmd.Dir = gitRoot
	branchCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	branchOut, _ := branchCmd.Output()
	currentBranch := strings.TrimSpace(string(branchOut))

	branchName := filepath.Base(worktreePath)

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

	deletedBranch := ""
	if branchName != "" && branchName != "main" && branchName != "master" {

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

		// #5212 gate: count commits that would be ORPHANED by `branch -D` -
		// neither reachable from the main HEAD nor present on any remote.
		// `--not --remotes` alone is too blunt: in a repo without remotes it
		// counts every branch as unpushed, which must not block cleaning a
		// creation branch whose commits all live on main (#4905 flow).
		orphanedCmd := exec.CommandContext(ctx, "git", "rev-list", "--count", branchName, "--not", "HEAD", "--not", "--remotes")
		orphanedCmd.Dir = mainRepoRoot
		orphanedCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
		orphanedOut, oerr := orphanedCmd.Output()
		orphaned, _ := strconv.Atoi(strings.TrimSpace(string(orphanedOut)))

		if uerr1 == nil && uerr2 == nil && oerr == nil && orphaned > 0 {
			// #5212: discard_changes consents to losing UNCOMMITTED FILE changes
			// only - it used to also silently authorize `branch -D` on branches
			// carrying unpushed/unmerged commits, which is how a completed
			// local fix (fix/issue-5201) got silently deleted. Deleting such a
			// branch now needs the dedicated force_delete_branch override, and
			// the keep path names the tip SHAs so the evidence is on screen.
			evidenceCmd := exec.CommandContext(ctx, "git", "rev-list", "--max-count=3", branchName)
			evidenceCmd.Dir = mainRepoRoot
			evidenceCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
			evidence, eerr := evidenceCmd.Output()
			evidenceLine := "(tip SHAs unavailable)"
			if eerr == nil && len(strings.TrimSpace(string(evidence))) > 0 {
				evidenceLine = strings.ReplaceAll(strings.TrimSpace(string(evidence)), "\n", " ")
			}
			if args.ForceDeleteBranch {
				delCmd := exec.CommandContext(ctx, "git", "branch", "-D", branchName)
				delCmd.Dir = mainRepoRoot
				delCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
				if err := delCmd.Run(); err == nil {
					deletedBranch = branchName
					result := fmt.Sprintf("Removed worktree %s", worktreePath)
					result += fmt.Sprintf(". WARNING: force-deleted branch %s carrying %d orphaned commit(s); tip SHAs: %s (recoverable via reflog for ~30 days)", branchName, orphaned, evidenceLine)
					return Result{
						Content:             result,
						SuggestedWorkingDir: mainRepoRoot,
					}, nil
				}
				return Result{
					IsError:             true,
					Content:             fmt.Sprintf("worktree removed, but force-deleting branch %s failed; branch kept (tip SHAs: %s)", branchName, evidenceLine),
					SuggestedWorkingDir: mainRepoRoot,
				}, nil
			}
			return Result{
				Content:             fmt.Sprintf("worktree removed, but branch %s has %d orphaned commit(s) (%d unmerged, %d unpushed); branch kept (#5212: discard_changes does not delete branches). tip SHAs: %s. To delete anyway, re-run with force_delete_branch=true; or push/merge first.", branchName, orphaned, unmerged, unpushed, evidenceLine),
				SuggestedWorkingDir: mainRepoRoot,
			}, nil
		}
		if uerr1 != nil || uerr2 != nil {
			debug.Log("tool.worktree", "branch safety probe failed for %s (unmerged err=%v unpushed err=%v); keeping branch", branchName, uerr1, uerr2)
		} else {
			delCmd := exec.CommandContext(ctx, "git", "branch", "-D", branchName)
			delCmd.Dir = mainRepoRoot
			delCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
			if err := delCmd.Run(); err == nil {
				deletedBranch = branchName
			}
		}
	}

	result := fmt.Sprintf("Removed worktree %s", worktreePath)
	if deletedBranch != "" {

		result += fmt.Sprintf(" and deleted branch %s (recoverable via reflog for ~30 days)", deletedBranch)
	}

	if currentBranch != "" && currentBranch != branchName && currentBranch != "main" && currentBranch != "master" {
		result += fmt.Sprintf(". Note: branch %s was checked out in the worktree (not created by enter_worktree); it was kept - delete it manually if unwanted", currentBranch)
	}

	return Result{
		Content:             result,
		SuggestedWorkingDir: mainRepoRoot,
	}, nil
}

func isWorktreeNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
}

func isSafeWorktreeName(name string) bool {
	if name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, string(os.PathSeparator)) {
		return false
	}
	return true
}

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

func isInsideWorktree(gitDir string) (bool, string, error) {
	absPath, _ := filepath.Abs(gitDir)

	gitFile := filepath.Join(gitDir, ".git")
	info, err := os.Lstat(gitFile)
	if err != nil || !info.Mode().IsRegular() {

		return false, "", nil
	}
	data, err := os.ReadFile(gitFile)
	if err != nil || !strings.HasPrefix(string(data), "gitdir: ") {

		return false, "", nil
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(gitDir, gitdir)
	}

	mainRoot := filepath.Dir(filepath.Dir(filepath.Dir(gitdir)))

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

func isWorktreeCheckout(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && !fi.IsDir()
}

func findGitRootFromWorktree(worktreePath string) (string, error) {
	gitFile := filepath.Join(worktreePath, ".git")
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", err
	}

	gitdir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))

	gitDir := filepath.Dir(filepath.Dir(filepath.Dir(gitdir)))
	return gitDir, nil
}

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

func checkWorktreeDirty(ctx context.Context, entries []worktreeEntry) {
	for i := range entries {
		stCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
		stCmd.Dir = entries[i].Path
		stCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
		stOut, stErr := stCmd.Output()
		if stErr == nil && len(strings.TrimSpace(string(stOut))) > 0 {
			entries[i].Dirty = true
		}

		if stErr != nil {
			debug.Log("tool.worktree", "dirty probe failed for %s: %v; marking dirty (fail-safe)", entries[i].Path, stErr)
			entries[i].Dirty = true
		}
	}
}

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

func (t ListWorktree) Clone() Tool {
	return &ListWorktree{WorkingDir: t.WorkingDir}
}

func (t EnterWorktree) Clone() Tool {
	return &EnterWorktree{WorkingDir: t.WorkingDir}
}

func (t ExitWorktree) Clone() Tool {
	return &ExitWorktree{WorkingDir: t.WorkingDir}
}
