package harness

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AutoInitResult contains the outcome of a minimal harness initialization.
type AutoInitResult struct {
	Project      Project
	Config       *Config
	ConfigPath   string
	StateDir     string
	CreatedPaths []string
}

// MinimalAutoInitConfig returns a harness config suitable for auto-initialized
// projects. Unlike DefaultConfig, it has no required files (no AGENTS.md),
// no content rules, and no check commands. This ensures that an auto-init'd
// project passes its own checks out of the box.
func MinimalAutoInitConfig(projectName string) *Config {
	return &Config{
		Version: 1,
		Project: ProjectConfig{
			Name: projectName,
			Goal: "Auto-initialized harness project",
			Deliverables: []string{
				"Working implementation",
				"Runnable validation",
			},
		},
		Checks: CheckConfig{
			// No required files — auto-init does not create AGENTS.md.
			// No content rules — no scaffold files to validate.
			// No commands — the user hasn't configured any yet.
		},
		Run: RunConfig{
			Mode:         "subagent",
			WorktreeMode: "auto",
		},
	}
}

// AutoInit performs a minimal harness initialization suitable for automatic
// routing. Unlike InitProject, it does NOT:
//   - Create AGENTS.md or context AGENTS.md files
//   - Create runbook documentation
//   - Run context detection
//   - Create a scaffold git commit
//
// It only creates the minimum needed for harness routing:
//   - .ggcode/harness.yaml with sensible defaults
//   - .ggcode/harness/ state directories (state, tasks, logs, archive)
func AutoInit(projectDir string) (*AutoInitResult, error) {
	root, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve project dir: %w", err)
	}

	// Check if already initialized
	existingConfig := filepath.Join(root, ".ggcode", "harness.yaml")
	if _, err := os.Stat(existingConfig); err == nil {
		return nil, fmt.Errorf("harness already initialized at %s", existingConfig)
	}

	project := projectFromRoot(root)
	projectName := filepath.Base(root)

	cfg := MinimalAutoInitConfig(projectName)

	// Create directories
	dirs := []string{
		project.StateDir,
		project.TasksDir,
		project.LogsDir,
		project.ArchiveDir,
	}
	var created []string
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
		created = append(created, dir)
	}

	// Write minimal harness.yaml
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(project.ConfigPath), 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(project.ConfigPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}
	created = append(created, project.ConfigPath)

	// Bootstrap harness state (queue, index, etc.)
	if err := bootstrapHarnessState(project); err != nil {
		// Non-fatal: the state files are optional for routing
		_ = err
	}

	return &AutoInitResult{
		Project:      project,
		Config:       cfg,
		ConfigPath:   project.ConfigPath,
		StateDir:     project.StateDir,
		CreatedPaths: created,
	}, nil
}
