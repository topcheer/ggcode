package harness

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type Workspace struct {
	Path   string
	Mode   string
	Branch string
}

type DirtyWorkspaceCheckpoint struct {
	DirtyPaths    []string
	Summary       string
	CommitMessage string
}

type ConfirmDirtyWorkspaceFunc func(DirtyWorkspaceCheckpoint) (bool, error)

type WorkspacePrepareOptions struct {
	ConfirmDirtyWorkspace ConfirmDirtyWorkspaceFunc
}

type checkpointDeclinedError struct {
	message string
}

func (e checkpointDeclinedError) Error() string {
	return e.message
}

func isCheckpointDeclinedError(err error) bool {
	var target checkpointDeclinedError
	return errors.As(err, &target)
}

type sharedRuntimeDirRule struct {
	name      string
	matchName string
	validate  func(string) bool
}

var sharedRuntimeDirRules = []sharedRuntimeDirRule{
	{
		name:      "node modules",
		matchName: "node_modules",
		validate: func(path string) bool {
			return true
		},
	},
	{
		name:      "python virtualenv",
		matchName: ".venv",
		validate:  isPythonVirtualEnvDir,
	},
	{
		name:      "python virtualenv",
		matchName: "venv",
		validate:  isPythonVirtualEnvDir,
	},
	{
		name:      "python virtualenv",
		matchName: "env",
		validate:  isPythonVirtualEnvDir,
	},
	{
		name:      "tox envs",
		matchName: ".tox",
		validate: func(path string) bool {
			entries, err := os.ReadDir(path)
			return err == nil && len(entries) > 0
		},
	},
}

var worktreeDirtyIgnoredPaths = []string{
	StateRelDir,
	".ggcode/todos.json",
}

func PrepareWorkspace(ctx context.Context, project Project, cfg *Config, task *Task, opts ...WorkspacePrepareOptions) (*Workspace, error) {
	var prepareOpts WorkspacePrepareOptions
	if len(opts) > 0 {
		prepareOpts = opts[0]
	}
	mode := "auto"
	if cfg != nil {
		mode = strings.TrimSpace(cfg.Run.WorktreeMode)
	}
	if mode == "" {
		mode = "auto"
	}
	if mode == "off" {
		return &Workspace{Path: project.RootDir, Mode: "root"}, nil
	}
	if _, err := os.Stat(filepath.Join(project.RootDir, ".git")); err != nil {
		if mode == "required" {
			return nil, fmt.Errorf("worktree mode is required but repository is not a git repo")
		}
		return &Workspace{Path: project.RootDir, Mode: "root"}, nil
	}
	if err := checkpointDirtyWorktreeBase(ctx, project.RootDir, prepareOpts.ConfirmDirtyWorkspace); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(project.WorktreesDir, 0755); err != nil {
		return nil, fmt.Errorf("create worktrees dir: %w", err)
	}
	branch := "harness-" + task.ID
	baseRef := "HEAD"
	if cfg != nil && strings.TrimSpace(cfg.Run.WorktreeBaseBranch) != "" {
		baseRef = strings.TrimSpace(cfg.Run.WorktreeBaseBranch)
	}
	workspacePath := filepath.Join(project.WorktreesDir, task.ID)
	cmd := gitCmd(ctx, "worktree", "add", "-B", branch, workspacePath, baseRef)
	cmd.Dir = project.RootDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if mode == "required" {
			return nil, fmt.Errorf("create git worktree: %s", strings.TrimSpace(string(out)))
		}
		return &Workspace{Path: project.RootDir, Mode: "root"}, fmt.Errorf("worktree unavailable, falling back to repo root: %s", strings.TrimSpace(string(out)))
	}
	if err := bootstrapWorkspaceRuntimeState(project.RootDir, workspacePath); err != nil {
		removeErr := removeGitWorktree(project.RootDir, workspacePath)
		if mode == "required" {
			if removeErr != nil {
				return nil, fmt.Errorf("bootstrap git worktree runtime state: %v (cleanup failed: %v)", err, removeErr)
			}
			return nil, fmt.Errorf("bootstrap git worktree runtime state: %w", err)
		}
		if removeErr != nil {
			return &Workspace{Path: project.RootDir, Mode: "root"}, fmt.Errorf("worktree runtime bootstrap failed, falling back to repo root: %v (cleanup failed: %v)", err, removeErr)
		}
		return &Workspace{Path: project.RootDir, Mode: "root"}, fmt.Errorf("worktree runtime bootstrap failed, falling back to repo root: %v", err)
	}
	return &Workspace{Path: workspacePath, Mode: "git-worktree", Branch: branch}, nil
}

func cleanupWorkspace(project Project, task *Task) error {
	if task == nil || strings.TrimSpace(task.WorkspacePath) == "" || task.WorkspacePath == project.RootDir {
		return nil
	}
	return removeGitWorktree(project.RootDir, task.WorkspacePath)
}

func removeGitWorktree(rootDir, workspacePath string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", workspacePath)
	cmd.Dir = rootDir
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(workspacePath)
		if strings.TrimSpace(string(out)) != "" {
			return fmt.Errorf("remove worktree: %s", strings.TrimSpace(string(out)))
		}
		return err
	}
	return nil
}

func bootstrapWorkspaceRuntimeState(rootDir, workspacePath string) error {
	runtimeDirs, err := discoverSharedRuntimeDirs(rootDir)
	if err != nil {
		return err
	}
	for _, rel := range runtimeDirs {
		if err := linkWorkspaceRuntimeDir(rootDir, workspacePath, rel); err != nil {
			return err
		}
	}
	return nil
}

func discoverSharedRuntimeDirs(rootDir string) ([]string, error) {
	var relPaths []string
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path == rootDir {
			return nil
		}
		name := d.Name()
		switch name {
		case ".git", ".ggcode":
			return filepath.SkipDir
		}
		for _, rule := range sharedRuntimeDirRules {
			if name != rule.matchName || !rule.validate(path) {
				continue
			}
			rel, err := filepath.Rel(rootDir, path)
			if err != nil {
				return fmt.Errorf("resolve %s dir %s: %w", rule.name, path, err)
			}
			relPaths = append(relPaths, rel)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover shared runtime dirs: %w", err)
	}
	slices.Sort(relPaths)
	return relPaths, nil
}

func isPythonVirtualEnvDir(path string) bool {
	if info, err := os.Stat(filepath.Join(path, "pyvenv.cfg")); err == nil && !info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(path, "bin", "python")); err == nil && !info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(path, "Scripts", "python.exe")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

func linkWorkspaceRuntimeDir(rootDir, workspacePath, rel string) error {
	source := filepath.Join(rootDir, rel)
	target := filepath.Join(workspacePath, rel)
	info, err := os.Lstat(target)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(target)
			if err == nil && resolved == source {
				return nil
			}
		}
		return nil
	case !os.IsNotExist(err):
		return fmt.Errorf("inspect runtime dir %s: %w", target, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return fmt.Errorf("create runtime dir parent %s: %w", target, err)
	}
	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("link runtime dir %s -> %s: %w", target, source, err)
	}
	return nil
}

func checkpointDirtyWorktreeBase(ctx context.Context, rootDir string, confirm ConfirmDirtyWorkspaceFunc) error {
	dirtyPaths, err := gitDirtyProjectPaths(ctx, rootDir)
	if err != nil {
		return err
	}
	if len(dirtyPaths) == 0 {
		return nil
	}
	checkpoint := DirtyWorkspaceCheckpoint{
		DirtyPaths:    append([]string(nil), dirtyPaths...),
		Summary:       summarizeDirtyPaths(dirtyPaths, 8),
		CommitMessage: buildWorktreeCheckpointMessage(dirtyPaths),
	}
	if confirm != nil {
		approved, err := confirm(checkpoint)
		if err != nil {
			return err
		}
		if !approved {
			return checkpointDeclinedError{message: "harness run cancelled: workspace checkpoint was not approved"}
		}
	}
	if err := autoCommitWorktreeBase(ctx, rootDir, dirtyPaths); err != nil {
		return fmt.Errorf("checkpoint dirty workspace before harness run: %w", err)
	}
	return nil
}

func gitDirtyProjectPaths(ctx context.Context, rootDir string) ([]string, error) {
	sharedRuntimeDirs, err := discoverSharedRuntimeDirs(rootDir)
	if err != nil {
		return nil, err
	}
	cmd := gitCmd(ctx, "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = rootDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("inspect git status: %s", strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	var paths []string
	seen := make(map[string]struct{})
	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" || len(raw) < 4 {
			continue
		}
		path := strings.TrimSpace(raw[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		path = filepath.ToSlash(path)
		if shouldIgnoreWorktreeDirtyPath(path, sharedRuntimeDirs) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func shouldIgnoreWorktreeDirtyPath(path string, sharedRuntimeDirs []string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return true
	}
	for _, ignored := range worktreeDirtyIgnoredPaths {
		ignored = filepath.ToSlash(strings.TrimSpace(ignored))
		if path == ignored || strings.HasPrefix(path, ignored+"/") {
			return true
		}
	}
	for _, rel := range sharedRuntimeDirs {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		if path == rel || strings.HasPrefix(path, rel+"/") {
			return true
		}
	}
	return false
}

func summarizeDirtyPaths(paths []string, limit int) string {
	if len(paths) == 0 {
		return ""
	}
	if limit <= 0 || len(paths) <= limit {
		return strings.Join(paths, ", ")
	}
	head := append([]string(nil), paths[:limit]...)
	return fmt.Sprintf("%s (+%d more)", strings.Join(head, ", "), len(paths)-limit)
}

func autoCommitWorktreeBase(ctx context.Context, rootDir string, dirtyPaths []string) error {
	if len(dirtyPaths) == 0 {
		return nil
	}
	addArgs := append([]string{"add", "-A", "--"}, dirtyPaths...)
	addCmd := gitCmd(ctx, addArgs...)
	addCmd.Dir = rootDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stage dirty workspace paths (%s): %s", summarizeDirtyPaths(dirtyPaths, 8), strings.TrimSpace(string(out)))
	}
	hasStaged, err := gitHasStagedChanges(ctx, rootDir)
	if err != nil {
		return err
	}
	if !hasStaged {
		return nil
	}
	message := buildWorktreeCheckpointMessage(dirtyPaths)
	commitArgs := []string{"commit", "--quiet", "--no-verify", "-m", message + harnessCoAuthor}
	commitArgs = append(commitAuthorConfig(ctx, rootDir), commitArgs...)
	commitCmd := gitCmd(ctx, commitArgs...)
	commitCmd.Dir = rootDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create checkpoint commit: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func buildWorktreeCheckpointMessage(paths []string) string {
	base := "chore: checkpoint workspace before harness run"
	if len(paths) == 0 {
		return base
	}
	label := summarizeDirtyPaths(paths, 2)
	message := fmt.Sprintf("%s (%s)", base, label)
	if len(message) <= 72 {
		return message
	}
	return base
}
