package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ConfigRelPath = ".ggcode/harness.yaml"
	StateRelDir   = ".ggcode/harness"
)

type Config struct {
	Version  int             `yaml:"version"`
	Project  ProjectConfig   `yaml:"project"`
	Checks   CheckConfig     `yaml:"checks"`
	Run      RunConfig       `yaml:"run"`
	GC       GCConfig        `yaml:"gc"`
	Contexts []ContextConfig `yaml:"contexts,omitempty"`
}

type ProjectConfig struct {
	Name         string   `yaml:"name"`
	Goal         string   `yaml:"goal"`
	Deliverables []string `yaml:"deliverables,omitempty"`
}

type ContextConfig struct {
	Name         string         `yaml:"name"`
	Path         string         `yaml:"path"`
	Description  string         `yaml:"description,omitempty"`
	Owner        string         `yaml:"owner,omitempty"`
	RequireAgent bool           `yaml:"require_agent,omitempty"`
	Commands     []CommandCheck `yaml:"commands,omitempty"`
}

type CheckConfig struct {
	RequiredFiles []string       `yaml:"required_files,omitempty"`
	RequiredDirs  []string       `yaml:"required_dirs,omitempty"`
	ContentRules  []ContentRule  `yaml:"content_rules,omitempty"`
	Commands      []CommandCheck `yaml:"commands,omitempty"`
}

type ContentRule struct {
	Path     string   `yaml:"path"`
	Contains []string `yaml:"contains,omitempty"`
}

type CommandCheck struct {
	Name     string `yaml:"name"`
	Run      string `yaml:"run"`
	Optional bool   `yaml:"optional,omitempty"`
}

type RunConfig struct {
	Mode               string `yaml:"mode"`
	MaxIterations      int    `yaml:"max_iterations"`
	MaxAttempts        int    `yaml:"max_attempts"`
	ExecutionMode      string `yaml:"execution_mode,omitempty"`
	PromptPreamble     string `yaml:"prompt_preamble,omitempty"`
	WorktreeMode       string `yaml:"worktree_mode,omitempty"`
	WorktreeBaseBranch string `yaml:"worktree_base_branch,omitempty"`
}

type GCConfig struct {
	AbandonAfter    string `yaml:"abandon_after,omitempty"`
	ArchiveAfter    string `yaml:"archive_after,omitempty"`
	DeleteLogsAfter string `yaml:"delete_logs_after,omitempty"`
}

func DefaultConfig(projectName, goal string) *Config {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		projectName = "project"
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		goal = "Deliver a production-ready software system with clear architecture, runnable validation, and explicit operating guidance."
	}
	return &Config{
		Version: 1,
		Project: ProjectConfig{
			Name: projectName,
			Goal: goal,
			Deliverables: []string{
				"Working implementation aligned with AGENTS.md guidance",
				"Runnable validation commands that prove the result",
				"Concise operating notes for future agents",
			},
		},
		Checks: CheckConfig{
			RequiredFiles: []string{
				"AGENTS.md",
				ConfigRelPath,
			},
			RequiredDirs: []string{
				".ggcode",
				StateRelDir,
			},
			ContentRules: []ContentRule{
				{
					Path: "AGENTS.md",
					Contains: []string{
						"Mission",
						"Architecture",
						"Quality Gates",
					},
				},
			},
			Commands: []CommandCheck{
				{
					Name: "status",
					Run:  "git --no-pager status --short",
				},
			},
		},
		Run: RunConfig{
			Mode:               "autopilot",
			MaxIterations:      40,
			MaxAttempts:        3,
			ExecutionMode:      "subagent",
			PromptPreamble:     "Operate in harness mode: read repository guidance first, keep work incremental, update tests/docs when needed, and run the configured checks before declaring completion.",
			WorktreeMode:       "auto",
			WorktreeBaseBranch: "",
		},
		GC: GCConfig{
			AbandonAfter:    "24h",
			ArchiveAfter:    "168h",
			DeleteLogsAfter: "336h",
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read harness config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse harness config: %w", err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if strings.TrimSpace(cfg.Run.Mode) == "" {
		cfg.Run.Mode = "autopilot"
	}
	if cfg.Run.MaxIterations <= 0 {
		cfg.Run.MaxIterations = 40
	}
	if cfg.Run.MaxAttempts <= 0 {
		cfg.Run.MaxAttempts = 3
	}
	if strings.TrimSpace(cfg.Run.ExecutionMode) == "" {
		cfg.Run.ExecutionMode = "subagent"
	}
	if strings.TrimSpace(cfg.Run.WorktreeMode) == "" {
		cfg.Run.WorktreeMode = "auto"
	}
	if strings.TrimSpace(cfg.GC.AbandonAfter) == "" {
		cfg.GC.AbandonAfter = "24h"
	}
	if strings.TrimSpace(cfg.GC.ArchiveAfter) == "" {
		cfg.GC.ArchiveAfter = "168h"
	}
	if strings.TrimSpace(cfg.GC.DeleteLogsAfter) == "" {
		cfg.GC.DeleteLogsAfter = "336h"
	}
	return &cfg, nil
}

func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal harness config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create harness config dir: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

func parseConfigDuration(raw string, fallback time.Duration) time.Duration {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
