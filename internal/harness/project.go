package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// gitCmd creates a git command with GIT_PAGER=cat.
func gitCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	return cmd
}

const harnessCoAuthor = "\n\nCo-Authored-By: ggcode <noreply@ggcode.dev>"

type Project struct {
	RootDir      string
	ConfigPath   string
	StateDir     string
	TasksDir     string
	LogsDir      string
	ArchiveDir   string
	WorktreesDir string
	EventLogPath string
	SnapshotPath string
}

type InitOptions struct {
	Goal     string
	Force    bool
	Contexts []ContextConfig
}

type InitResult struct {
	Project        Project
	CreatedPaths   []string
	Overwritten    []string
	Config         *Config
	GitInitialized bool
	ScaffoldCommit string
}

func Discover(startDir string) (Project, error) {
	startDir = strings.TrimSpace(startDir)
	if startDir == "" {
		return Project{}, fmt.Errorf("missing working directory")
	}
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return Project{}, fmt.Errorf("resolve working directory: %w", err)
	}
	current := abs
	for {
		candidate := filepath.Join(current, ConfigRelPath)
		if _, err := os.Stat(candidate); err == nil {
			return projectFromRoot(current), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return Project{}, fmt.Errorf("no harness project found from %s upward; run `ggcode harness init` first", abs)
}

func Init(dir string, opts InitOptions) (*InitResult, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	gitInitialized, err := ensureGitRepository(context.Background(), root)
	if err != nil {
		return nil, err
	}
	project := projectFromRoot(root)
	projectName := filepath.Base(root)
	cfg := DefaultConfig(projectName, opts.Goal)
	if len(opts.Contexts) > 0 {
		cfg.Contexts = NormalizeContexts(opts.Contexts)
	} else {
		cfg.Contexts = detectContexts(root)
	}

	if err := os.MkdirAll(project.StateDir, 0755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(project.TasksDir, 0755); err != nil {
		return nil, fmt.Errorf("create tasks dir: %w", err)
	}
	if err := os.MkdirAll(project.LogsDir, 0755); err != nil {
		return nil, fmt.Errorf("create logs dir: %w", err)
	}
	if err := os.MkdirAll(project.ArchiveDir, 0755); err != nil {
		return nil, fmt.Errorf("create archive dir: %w", err)
	}
	if err := bootstrapHarnessState(project); err != nil {
		return nil, err
	}

	result := &InitResult{Project: project, Config: cfg, GitInitialized: gitInitialized}
	created, overwritten, err := writeTemplateFile(project.ConfigPath, opts.Force, renderConfigTemplate(cfg))
	if err != nil {
		return nil, err
	}
	collectInitPath(result, project.ConfigPath, created, overwritten)

	agentsPath := filepath.Join(project.RootDir, "AGENTS.md")
	created, overwritten, err = writeTemplateFile(agentsPath, opts.Force, renderAgentsTemplate(cfg))
	if err != nil {
		return nil, err
	}
	collectInitPath(result, agentsPath, created, overwritten)

	runbookPath := filepath.Join(project.RootDir, "docs", "runbooks", "harness.md")
	created, overwritten, err = writeTemplateFile(runbookPath, opts.Force, renderRunbookTemplate(cfg))
	if err != nil {
		return nil, err
	}
	collectInitPath(result, runbookPath, created, overwritten)

	for _, contextCfg := range cfg.Contexts {
		if !contextCfg.RequireAgent || strings.TrimSpace(contextCfg.Path) == "" {
			continue
		}
		agentPath := filepath.Join(project.RootDir, contextCfg.Path, "AGENTS.md")
		created, overwritten, err = writeTemplateFile(agentPath, opts.Force, renderContextAgentsTemplate(cfg, contextCfg))
		if err != nil {
			return nil, err
		}
		collectInitPath(result, agentPath, created, overwritten)
	}

	if commit, err := commitInitialHarnessScaffold(context.Background(), project.RootDir, result.CreatedPaths, result.Overwritten); err != nil {
		return nil, err
	} else {
		result.ScaffoldCommit = commit
	}

	return result, nil
}

func ensureGitRepository(ctx context.Context, root string) (bool, error) {
	if isGitWorkingTree(ctx, root) {
		return false, nil
	}
	cmd := gitCmd(ctx, "init", "--quiet", root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("initialize git repository: %s", strings.TrimSpace(string(out)))
	}
	if !isGitWorkingTree(ctx, root) {
		return false, fmt.Errorf("initialize git repository: git init completed but %s is still not a git repository", root)
	}
	return true, nil
}

func commitInitialHarnessScaffold(ctx context.Context, root string, created, overwritten []string) (string, error) {
	if hasHeadCommit(ctx, root) {
		return "", nil
	}
	paths := dedupeExistingPaths(root, append(append([]string(nil), created...), overwritten...))
	if len(paths) == 0 {
		return "", nil
	}
	addCmd := gitCmd(ctx, append([]string{"add", "--"}, paths...)...)
	addCmd.Dir = root
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("stage harness scaffold: %s", strings.TrimSpace(string(out)))
	}
	commitArgs := []string{"commit", "--quiet", "--no-verify", "-m", "chore: initialize harness scaffold" + harnessCoAuthor}
	commitArgs = append(commitAuthorConfig(ctx, root), commitArgs...)
	commitCmd := gitCmd(ctx, commitArgs...)
	commitCmd.Dir = root
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("commit harness scaffold: %s", strings.TrimSpace(string(out)))
	}
	return gitHeadCommit(ctx, root)
}

func projectFromRoot(root string) Project {
	stateDir := filepath.Join(root, StateRelDir)
	return Project{
		RootDir:      root,
		ConfigPath:   filepath.Join(root, ConfigRelPath),
		StateDir:     stateDir,
		TasksDir:     filepath.Join(stateDir, "tasks"),
		LogsDir:      filepath.Join(stateDir, "logs"),
		ArchiveDir:   filepath.Join(stateDir, "archive"),
		WorktreesDir: filepath.Join(stateDir, "worktrees"),
		EventLogPath: filepath.Join(stateDir, "events.jsonl"),
		SnapshotPath: filepath.Join(stateDir, "snapshot.db"),
	}
}

func writeTemplateFile(path string, force bool, content string) (created bool, overwritten bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, false, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		if !force {
			return false, false, nil
		}
		overwritten = true
	} else if !os.IsNotExist(statErr) {
		return false, false, fmt.Errorf("stat %s: %w", path, statErr)
	} else {
		created = true
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, false, fmt.Errorf("write %s: %w", path, err)
	}
	return created, overwritten, nil
}

func collectInitPath(result *InitResult, path string, created bool, overwritten bool) {
	if result == nil {
		return
	}
	if created {
		result.CreatedPaths = append(result.CreatedPaths, path)
	}
	if overwritten {
		result.Overwritten = append(result.Overwritten, path)
	}
}

func hasHeadCommit(ctx context.Context, root string) bool {
	cmd := gitCmd(ctx, "rev-parse", "--verify", "HEAD")
	cmd.Dir = root
	return cmd.Run() == nil
}

func gitHeadCommit(ctx context.Context, root string) (string, error) {
	cmd := gitCmd(ctx, "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve harness scaffold commit: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func commitAuthorConfig(ctx context.Context, root string) []string {
	nameCmd := gitCmd(ctx, "config", "--get", "user.name")
	nameCmd.Dir = root
	emailCmd := gitCmd(ctx, "config", "--get", "user.email")
	emailCmd.Dir = root
	if nameCmd.Run() == nil && emailCmd.Run() == nil {
		return nil
	}
	return []string{"-c", "user.name=ggcode harness", "-c", "user.email=harness@ggcode.local"}
}

func dedupeExistingPaths(root string, paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		rel = filepath.Clean(rel)
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

func detectContexts(root string) []ContextConfig {
	candidates := []ContextConfig{
		{Name: "application-entrypoints", Path: "cmd", Description: "Application entrypoints and binaries", RequireAgent: true},
	}

	internalDir := filepath.Join(root, "internal")
	entries, err := os.ReadDir(internalDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			candidates = append(candidates, ContextConfig{
				Name:         "internal-" + name,
				Path:         filepath.Join("internal", name),
				Description:  fmt.Sprintf("Bounded context for %s", strings.ReplaceAll(name, "-", " ")),
				RequireAgent: true,
			})
		}
	}

	var filtered []ContextConfig
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate.Path = filepath.Clean(candidate.Path)
		if candidate.Path == "." || candidate.Path == "" {
			continue
		}
		if _, ok := seen[candidate.Path]; ok {
			continue
		}
		info, err := os.Stat(filepath.Join(root, candidate.Path))
		if err != nil || !info.IsDir() {
			continue
		}
		seen[candidate.Path] = struct{}{}
		filtered = append(filtered, candidate)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Path < filtered[j].Path
	})
	return NormalizeContexts(filtered)
}

// DetectContexts returns heuristic context suggestions from the current repo tree.
func DetectContexts(root string) []ContextConfig {
	return detectContexts(root)
}
