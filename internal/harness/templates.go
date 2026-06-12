package harness

import (
	"fmt"
	"strings"
)

func renderConfigTemplate(cfg *Config) string {
	var b strings.Builder
	b.WriteString("version: 1\n")
	b.WriteString("project:\n")
	b.WriteString(fmt.Sprintf("  name: %q\n", cfg.Project.Name))
	b.WriteString(fmt.Sprintf("  goal: %q\n", cfg.Project.Goal))
	if len(cfg.Project.Deliverables) > 0 {
		b.WriteString("  deliverables:\n")
		for _, item := range cfg.Project.Deliverables {
			b.WriteString(fmt.Sprintf("    - %q\n", item))
		}
	}
	b.WriteString("checks:\n")
	if len(cfg.Checks.RequiredFiles) > 0 {
		b.WriteString("  required_files:\n")
		for _, item := range cfg.Checks.RequiredFiles {
			b.WriteString(fmt.Sprintf("    - %q\n", item))
		}
	}
	if len(cfg.Checks.RequiredDirs) > 0 {
		b.WriteString("  required_dirs:\n")
		for _, item := range cfg.Checks.RequiredDirs {
			b.WriteString(fmt.Sprintf("    - %q\n", item))
		}
	}
	if len(cfg.Checks.ContentRules) > 0 {
		b.WriteString("  content_rules:\n")
		for _, rule := range cfg.Checks.ContentRules {
			b.WriteString(fmt.Sprintf("    - path: %q\n", rule.Path))
			if len(rule.Contains) > 0 {
				b.WriteString("      contains:\n")
				for _, item := range rule.Contains {
					b.WriteString(fmt.Sprintf("        - %q\n", item))
				}
			}
		}
	}
	if len(cfg.Checks.Commands) > 0 {
		b.WriteString("  commands:\n")
		for _, cmd := range cfg.Checks.Commands {
			b.WriteString(fmt.Sprintf("    - name: %q\n", cmd.Name))
			b.WriteString(fmt.Sprintf("      run: %q\n", cmd.Run))
			if cmd.Optional {
				b.WriteString("      optional: true\n")
			}
		}
	}
	b.WriteString("run:\n")
	b.WriteString(fmt.Sprintf("  mode: %q\n", cfg.Run.Mode))
	b.WriteString(fmt.Sprintf("  max_attempts: %d\n", cfg.Run.MaxAttempts))
	b.WriteString(fmt.Sprintf("  execution_mode: %q\n", cfg.Run.ExecutionMode))
	b.WriteString(fmt.Sprintf("  prompt_preamble: %q\n", cfg.Run.PromptPreamble))
	b.WriteString(fmt.Sprintf("  worktree_mode: %q\n", cfg.Run.WorktreeMode))
	b.WriteString(fmt.Sprintf("  worktree_base_branch: %q\n", cfg.Run.WorktreeBaseBranch))
	b.WriteString("gc:\n")
	b.WriteString(fmt.Sprintf("  abandon_after: %q\n", cfg.GC.AbandonAfter))
	b.WriteString(fmt.Sprintf("  archive_after: %q\n", cfg.GC.ArchiveAfter))
	b.WriteString(fmt.Sprintf("  delete_logs_after: %q\n", cfg.GC.DeleteLogsAfter))
	if len(cfg.Contexts) > 0 {
		b.WriteString("contexts:\n")
		for _, context := range cfg.Contexts {
			b.WriteString(fmt.Sprintf("  - name: %q\n", context.Name))
			if strings.TrimSpace(context.Path) != "" {
				b.WriteString(fmt.Sprintf("    path: %q\n", context.Path))
			}
			if context.Description != "" {
				b.WriteString(fmt.Sprintf("    description: %q\n", context.Description))
			}
			if context.Owner != "" {
				b.WriteString(fmt.Sprintf("    owner: %q\n", context.Owner))
			}
			if context.RequireAgent && strings.TrimSpace(context.Path) != "" {
				b.WriteString("    require_agent: true\n")
			}
			if len(context.Commands) > 0 {
				b.WriteString("    commands:\n")
				for _, cmd := range context.Commands {
					b.WriteString(fmt.Sprintf("      - name: %q\n", cmd.Name))
					b.WriteString(fmt.Sprintf("        run: %q\n", cmd.Run))
					if cmd.Optional {
						b.WriteString("        optional: true\n")
					}
				}
			}
		}
	}
	return b.String()
}

func renderAgentsTemplate(cfg *Config) string {
	return fmt.Sprintf(`# AGENTS.md

## Mission
- Project: %s
- Objective: %s

## Architecture
- Keep the codebase modular and make structural changes intentionally.
- Prefer the smallest change that still keeps the system coherent.
- Update docs and validation commands when behavior changes.

## Quality Gates
- Read this file and .ggcode/harness.yaml before changing code.
- Keep work incremental and reversible.
- Run the configured harness checks before declaring success.
- Leave focused notes in docs/runbooks/harness.md when future agents need context.

## Delivery Checklist
- Implementation matches the current repository intent.
- Validation commands are runnable from the repo root.
- Repo-specific decisions are recorded close to the code.
`, cfg.Project.Name, cfg.Project.Goal)
}

func renderRunbookTemplate(cfg *Config) string {
	return fmt.Sprintf(`# Harness Runbook

## Current Goal
%s

## Operating Notes
- Use 'ggcode harness check' to verify the repository invariants.
- Use 'ggcode harness run "<goal>"' for tracked execution runs.
- Use 'ggcode harness doctor' for health/status inspection.
- Use 'ggcode harness gc' to archive stale runs and prune old logs.

## Repo-Specific Guidance
- Record commands, workflows, and caveats here as the project evolves.
`, cfg.Project.Goal)
}

func renderContextAgentsTemplate(cfg *Config, contextCfg ContextConfig) string {
	pathLine := "- Path: not bound yet"
	if strings.TrimSpace(contextCfg.Path) != "" {
		pathLine = "- Path: " + contextCfg.Path
	}
	return fmt.Sprintf(`# AGENTS.md

## Context
- Project: %s
- Area: %s
%s
%s

## Scope
- %s
- Keep changes in this subtree cohesive and well-tested.
- Escalate cross-cutting architectural changes back to the repository root guidance when needed.

## Local Quality Gates
- Read the root AGENTS.md and .ggcode/harness.yaml before changing this area.
- Preserve clear boundaries for this subtree.
- Add or update validation close to the code you change.
`, cfg.Project.Name, contextCfg.Name, pathLine, renderContextOwnerLine(contextCfg), firstNonEmptyText(contextCfg.Description, "Bounded context owned by the harness"))
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func renderContextOwnerLine(contextCfg ContextConfig) string {
	if strings.TrimSpace(contextCfg.Owner) == "" {
		return ""
	}
	return fmt.Sprintf("- Owner: %s", contextCfg.Owner)
}
