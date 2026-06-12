package harness

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/config"
)

// AutoRunResult contains the outcome of the auto-run decision process.
type AutoRunResult struct {
	// Decision is the routing decision.
	Decision RouteDecision
	// Project is the resolved harness project, if available.
	// nil if no project could be found or auto-initialized.
	Project *Project
	// Config is the loaded (and possibly overridden) harness config.
	// In strict mode, Run.WorktreeMode is overridden to "required".
	// The caller should use this config instead of re-reading from disk.
	Config *Config
	// AutoInitPerformed is true if a minimal harness was auto-initialized.
	AutoInitPerformed bool
	// StrictWriteGuard is true when the caller should block direct file
	// writes (write_file, edit_file, multi_edit_file) to the main project.
	// This is set when auto_run is "strict" and a harness project is available.
	// The caller should add Deny rules for write tools to the permission policy.
	StrictWriteGuard bool
	// Message is a human-readable message explaining the decision.
	// For RouteSuggest, this is the prompt to show the user.
	Message string
}

// ShouldAutoRun determines whether a user prompt should be routed to harness.
// It integrates the config auto_run mode, project discovery, auto-init, and
// the deterministic router.
//
// Returns an AutoRunResult with the decision and any resolved project.
// The caller is responsible for acting on the decision (e.g., showing a
// confirmation prompt for RouteSuggest, or directly starting a harness run
// for RouteHarness).
func ShouldAutoRun(cfg *config.Config, input string, ctx RouteContext) (*AutoRunResult, error) {
	if cfg == nil {
		return &AutoRunResult{Decision: RouteNone}, nil
	}

	mode := cfg.Harness.AutoRunMode()
	if mode == "off" {
		return &AutoRunResult{Decision: RouteNone}, nil
	}

	// Try to discover an existing harness project.
	var project *Project
	var autoInitPerformed bool
	if p, err := Discover(ctx.WorkingDir); err == nil {
		project = &p
		ctx.ProjectHasHarness = true
	} else if dir := ctx.WorkingDir; dir != "" {
		// No project found — try auto-init if configured.
		if cfg.Harness.AutoInit {
			initResult, err := AutoInit(dir)
			if err == nil {
				p := initResult.Project
				project = &p
				ctx.ProjectHasHarness = true
				autoInitPerformed = true
			}
			// Auto-init failure is non-fatal: fall through to routing
			// without a project. The router will still classify the input,
			// but without a project the caller can't actually run harness.
		}
	}

	// In strict mode, enforce worktree isolation by overriding the config.
	var strictConfig *Config
	if mode == "strict" && project != nil {
		loadedCfg, err := LoadConfig(project.ConfigPath)
		if err == nil {
			loadedCfg.Run.WorktreeMode = "required"
			strictConfig = loadedCfg
		}
	}

	// Run the deterministic router.
	decision := DecideRouteWithFeatures(input, mode, ExtractFeatures(input), ctx)

	result := &AutoRunResult{
		Decision:          decision,
		Project:           project,
		Config:            strictConfig,
		AutoInitPerformed: autoInitPerformed,
		StrictWriteGuard:  mode == "strict" && project != nil,
	}

	// Generate appropriate messages.
	switch decision {
	case RouteSuggest:
		result.Message = fmt.Sprintf(
			"This looks like a code change task. Run in harness for isolated execution?\n" +
				"  [Enter] Run in harness  [Esc] Chat normally",
		)
	case RouteHarness:
		if project == nil {
			// Can't route without a project — downgrade to suggest.
			result.Decision = RouteSuggest
			result.Message = "Harness project not available. Run `/harness init` to set up harness, or continue normally."
		}
	case RouteNormal:
		// No message needed for normal routing.
	}

	return result, nil
}
