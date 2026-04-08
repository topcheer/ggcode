package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/util"
)

type CheckIssue struct {
	Level   string
	Kind    string
	Path    string
	Message string
	Fix     string
}

type CommandResult struct {
	Name     string
	Scope    string
	Command  string
	Optional bool
	Success  bool
	Output   string
}

type CheckReport struct {
	Passed   bool
	Issues   []CheckIssue
	Commands []CommandResult
}

type CheckOptions struct {
	RunCommands bool
	CommandDir  string
	Context     string
}

func CheckProject(ctx context.Context, project Project, cfg *Config, opts CheckOptions) (*CheckReport, error) {
	report := &CheckReport{Passed: true}
	for _, rel := range cfg.Checks.RequiredFiles {
		path := filepath.Join(project.RootDir, rel)
		if _, err := os.Stat(path); err != nil {
			report.Passed = false
			report.Issues = append(report.Issues, CheckIssue{
				Level:   "error",
				Kind:    "missing-file",
				Path:    rel,
				Message: fmt.Sprintf("required file %s is missing", rel),
				Fix:     fmt.Sprintf("create %s or run `ggcode harness init --force`", rel),
			})
		}
	}
	for _, rel := range cfg.Checks.RequiredDirs {
		path := filepath.Join(project.RootDir, rel)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			report.Passed = false
			report.Issues = append(report.Issues, CheckIssue{
				Level:   "error",
				Kind:    "missing-dir",
				Path:    rel,
				Message: fmt.Sprintf("required directory %s is missing", rel),
				Fix:     fmt.Sprintf("create %s or run `ggcode harness init --force`", rel),
			})
		}
	}
	for _, rule := range cfg.Checks.ContentRules {
		path := filepath.Join(project.RootDir, rule.Path)
		data, err := os.ReadFile(path)
		if err != nil {
			report.Passed = false
			report.Issues = append(report.Issues, CheckIssue{
				Level:   "error",
				Kind:    "missing-content-source",
				Path:    rule.Path,
				Message: fmt.Sprintf("cannot read %s for content validation", rule.Path),
				Fix:     fmt.Sprintf("create %s before running harness checks", rule.Path),
			})
			continue
		}
		body := string(data)
		for _, needle := range rule.Contains {
			if strings.Contains(body, needle) {
				continue
			}
			report.Passed = false
			report.Issues = append(report.Issues, CheckIssue{
				Level:   "error",
				Kind:    "missing-content",
				Path:    rule.Path,
				Message: fmt.Sprintf("%s does not contain required text %q", rule.Path, needle),
				Fix:     fmt.Sprintf("add %q to %s", needle, rule.Path),
			})
		}
	}
	for _, contextCfg := range cfg.Contexts {
		contextPath := filepath.Join(project.RootDir, contextCfg.Path)
		info, err := os.Stat(contextPath)
		if err != nil || !info.IsDir() {
			report.Passed = false
			report.Issues = append(report.Issues, CheckIssue{
				Level:   "error",
				Kind:    "missing-context",
				Path:    contextCfg.Path,
				Message: fmt.Sprintf("context path %s is missing", contextCfg.Path),
				Fix:     fmt.Sprintf("restore %s or update contexts in .ggcode/harness.yaml", contextCfg.Path),
			})
			continue
		}
		if !contextCfg.RequireAgent {
			continue
		}
		agentPath := filepath.Join(contextPath, "AGENTS.md")
		if _, err := os.Stat(agentPath); err != nil {
			report.Passed = false
			report.Issues = append(report.Issues, CheckIssue{
				Level:   "error",
				Kind:    "missing-context-agent",
				Path:    filepath.Join(contextCfg.Path, "AGENTS.md"),
				Message: fmt.Sprintf("context %s is missing AGENTS.md", contextCfg.Path),
				Fix:     fmt.Sprintf("rerun `ggcode harness init --force` or add %s manually", filepath.Join(contextCfg.Path, "AGENTS.md")),
			})
		}
	}
	if opts.RunCommands {
		if err := runConfiguredCommands(ctx, project, cfg, opts, report); err != nil {
			return nil, err
		}
	}
	return report, nil
}

func runConfiguredCommands(ctx context.Context, project Project, cfg *Config, opts CheckOptions, report *CheckReport) error {
	commandDir := strings.TrimSpace(opts.CommandDir)
	if commandDir == "" {
		commandDir = project.RootDir
	}
	for _, check := range cfg.Checks.Commands {
		result, err := runCheckCommand(ctx, commandDir, "root", check)
		if err != nil {
			return err
		}
		report.Commands = append(report.Commands, *result)
		if result.Success || check.Optional {
			continue
		}
		report.Passed = false
		report.Issues = append(report.Issues, CheckIssue{
			Level:   "error",
			Kind:    "command-failed",
			Path:    check.Name,
			Message: fmt.Sprintf("check command %q failed", check.Name),
			Fix:     fmt.Sprintf("fix the failure and rerun `%s`", check.Run),
		})
	}
	filter := strings.TrimSpace(opts.Context)
	for _, contextCfg := range cfg.Contexts {
		if filter != "" && !contextMatches(contextCfg, filter) {
			continue
		}
		if len(contextCfg.Commands) == 0 {
			continue
		}
		contextDir := filepath.Join(commandDir, contextCfg.Path)
		for _, check := range contextCfg.Commands {
			result, err := runCheckCommand(ctx, contextDir, contextCfg.Name, check)
			if err != nil {
				return err
			}
			report.Commands = append(report.Commands, *result)
			if result.Success || check.Optional {
				continue
			}
			report.Passed = false
			report.Issues = append(report.Issues, CheckIssue{
				Level:   "error",
				Kind:    "context-command-failed",
				Path:    filepath.Join(contextCfg.Path, check.Name),
				Message: fmt.Sprintf("context command %q failed for %s", check.Name, contextCfg.Name),
				Fix:     fmt.Sprintf("fix the failure in %s and rerun `%s`", contextCfg.Path, check.Run),
			})
		}
	}
	return nil
}

func runCheckCommand(ctx context.Context, workingDir string, scope string, check CommandCheck) (*CommandResult, error) {
	cmd, _, err := util.NewShellCommandContext(ctx, check.Run)
	if err != nil {
		return nil, fmt.Errorf("build shell command %q: %w", check.Run, err)
	}
	cmd.Dir = workingDir
	out, runErr := cmd.CombinedOutput()
	result := &CommandResult{
		Name:     check.Name,
		Scope:    scope,
		Command:  check.Run,
		Optional: check.Optional,
		Success:  runErr == nil,
		Output:   strings.TrimSpace(string(out)),
	}
	return result, nil
}

func FormatCheckReport(report *CheckReport) string {
	if report == nil {
		return "No harness check report."
	}
	var b strings.Builder
	if report.Passed {
		b.WriteString("Harness check passed.\n")
	} else {
		b.WriteString("Harness check failed.\n")
	}
	if len(report.Issues) > 0 {
		b.WriteString("\nIssues:\n")
		for _, issue := range report.Issues {
			b.WriteString(fmt.Sprintf("- [%s] %s", issue.Kind, issue.Message))
			if strings.TrimSpace(issue.Fix) != "" {
				b.WriteString(fmt.Sprintf(" — fix: %s", issue.Fix))
			}
			b.WriteString("\n")
		}
	}
	if len(report.Commands) > 0 {
		b.WriteString("\nCommands:\n")
		for _, cmd := range report.Commands {
			status := "ok"
			if !cmd.Success {
				status = "failed"
			}
			label := cmd.Name
			if strings.TrimSpace(cmd.Scope) != "" {
				label = fmt.Sprintf("%s/%s", cmd.Scope, cmd.Name)
			}
			b.WriteString(fmt.Sprintf("- %s: %s (%s)\n", label, status, cmd.Command))
			if strings.TrimSpace(cmd.Output) != "" {
				b.WriteString(indentText(cmd.Output, "  "))
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func contextMatches(contextCfg ContextConfig, raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	return strings.EqualFold(contextCfg.Name, raw) || filepath.Clean(contextCfg.Path) == filepath.Clean(raw)
}
