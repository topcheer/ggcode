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

	cfg := DefaultConfig(projectName, "")

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
