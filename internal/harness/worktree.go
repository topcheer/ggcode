package harness

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type Workspace struct {
	Path   string
	Mode   string
	Branch string
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

func PrepareWorkspace(ctx context.Context, project Project, cfg *Config, task *Task) (*Workspace, error) {
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
	if err := os.MkdirAll(project.WorktreesDir, 0755); err != nil {
		return nil, fmt.Errorf("create worktrees dir: %w", err)
	}
	branch := "harness-" + task.ID
	baseRef := "HEAD"
	if cfg != nil && strings.TrimSpace(cfg.Run.WorktreeBaseBranch) != "" {
		baseRef = strings.TrimSpace(cfg.Run.WorktreeBaseBranch)
	}
	workspacePath := filepath.Join(project.WorktreesDir, task.ID)
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-B", branch, workspacePath, baseRef)
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
