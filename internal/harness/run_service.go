package harness

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CTAAction represents the next action a user should take after a harness run.
type CTAAction string

const (
	CTANone          CTAAction = ""
	CTAReview        CTAAction = "review"
	CTARerun         CTAAction = "rerun"
	CTAPromote       CTAAction = "promote"
	CTAInspectLog    CTAAction = "inspect-log"
	CTAInspectReport CTAAction = "inspect-report"
)

// RunServiceResult contains the result of a RunService execution plus
// a suggested next action (CTA) for the user.
type RunServiceResult struct {
	Summary *RunSummary
	Error   error
	// CTA is the recommended next action for the user.
	CTA CTAAction
	// CTAMessage is a human-readable description of what to do next.
	CTAMessage string
}

// RunServiceInput contains all parameters needed to execute a harness task.
type RunServiceInput struct {
	Project Project
	Config  *Config
	Goal    string
	Runner  Runner
	Options RunTaskOptions
}

// RunService provides a unified entry point for harness task execution.
// It wraps RunTaskWithOptions with config resolution, timeout management,
// and post-run CTA generation. All entry points (TUI, pipe, daemon) should
// use this service to ensure consistent behavior.
type RunService struct {
	// Timeout is the maximum duration for a single task run.
	// Defaults to 30 minutes if zero.
	Timeout time.Duration
}

// NewRunService creates a RunService with sensible defaults.
func NewRunService() *RunService {
	return &RunService{Timeout: 30 * time.Minute}
}

// Run executes a harness task and returns the result with a CTA.
func (s *RunService) Run(ctx context.Context, input RunServiceInput) *RunServiceResult {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 30 * time.Minute
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// If no ConfirmDirtyWorkspace is provided, use fail-fast: refuse to
	// auto-commit dirty workspace. This prevents auto-run from silently
	// modifying the user's working tree.
	if input.Options.ConfirmDirtyWorkspace == nil {
		input.Options.ConfirmDirtyWorkspace = func(checkpoint DirtyWorkspaceCheckpoint) (bool, error) {
			return false, fmt.Errorf("harness auto-run requires clean workspace (dirty: %d files). Commit or stash your changes first, or use /harness run interactively.", len(checkpoint.DirtyPaths))
		}
	}

	summary, err := RunTaskWithOptions(runCtx, input.Project, input.Config, input.Goal, input.Runner, input.Options)
	result := &RunServiceResult{
		Summary: summary,
		Error:   err,
	}

	// Generate CTA based on outcome
	result.CTA, result.CTAMessage = generateCTA(summary, err)
	return result
}

// FormatCTA returns a formatted string with the CTA for display.
func FormatCTA(result *RunServiceResult) string {
	if result == nil || result.CTA == CTANone || result.CTAMessage == "" {
		return ""
	}
	return fmt.Sprintf("\n📋 Next: %s", result.CTAMessage)
}

// GenerateCTA determines the next action based on task outcome.
// Exported for use by TUI handler to generate CTA for manual harness runs.
func GenerateCTA(summary *RunSummary, err error) (CTAAction, string) {
	return generateCTA(summary, err)
}

// generateCTA determines the next action based on task outcome.
func generateCTA(summary *RunSummary, err error) (CTAAction, string) {
	if err != nil {
		if summary != nil && summary.Task != nil {
			return CTARerun, fmt.Sprintf("Task %s failed. Run /harness rerun %s to retry.", summary.Task.ID, summary.Task.ID)
		}
		return CTARerun, "Task failed. Check the error above and retry."
	}

	if summary == nil || summary.Task == nil {
		return CTANone, ""
	}

	task := summary.Task

	switch task.Status {
	case TaskCompleted:
		if task.VerificationStatus == VerificationPassed {
			if task.ReviewStatus == ReviewPending || task.ReviewStatus == "" {
				return CTAReview, fmt.Sprintf("Task %s passed verification. Review changes with /harness review approve %s %s", task.ID, task.ID, shortNoteSuggestion(task))
			}
			if task.ReviewStatus == ReviewApproved {
				if task.PromotionStatus != PromotionApplied {
					return CTAPromote, fmt.Sprintf("Task %s is approved. Apply changes with /harness promote apply %s", task.ID, task.ID)
				}
				return CTANone, fmt.Sprintf("Task %s completed and promoted.", task.ID)
			}
		} else if task.VerificationStatus == VerificationFailed {
			return CTAInspectReport, fmt.Sprintf("Task %s failed verification. Check %s for details, then /harness rerun %s", task.ID, task.VerificationReportPath, task.ID)
		}
		// Completed without verification
		if task.ReviewStatus == ReviewPending || task.ReviewStatus == "" {
			return CTAReview, fmt.Sprintf("Task %s completed. Review with /harness review approve %s %s", task.ID, task.ID, shortNoteSuggestion(task))
		}
		return CTANone, fmt.Sprintf("Task %s completed.", task.ID)

	case TaskFailed:
		cta := fmt.Sprintf("Task %s failed", task.ID)
		if task.LogPath != "" {
			cta += fmt.Sprintf(". Check log: %s", task.LogPath)
			return CTAInspectLog, cta + fmt.Sprintf(". Retry with /harness rerun %s", task.ID)
		}
		return CTARerun, cta + fmt.Sprintf(". Retry with /harness rerun %s", task.ID)

	default:
		return CTANone, ""
	}
}

// FormatRunServiceResult renders a complete RunService result including
// the run summary and CTA for display.
func FormatRunServiceResult(result *RunServiceResult) string {
	if result == nil {
		return "No harness run executed."
	}

	var parts []string

	// Summary
	if result.Error != nil {
		parts = append(parts, fmt.Sprintf("Harness run error: %v", result.Error))
	}

	if result.Summary != nil {
		parts = append(parts, FormatRunSummary(result.Summary))
	}

	// CTA
	if cta := FormatCTA(result); cta != "" {
		parts = append(parts, cta)
	}

	return strings.Join(parts, "\n")
}

// shortNoteSuggestion returns a suggested --note flag for the review command.
func shortNoteSuggestion(task *Task) string {
	if task.Goal == "" {
		return "--note \"lgtm\""
	}
	goal := task.Goal
	if len(goal) > 40 {
		goal = goal[:37] + "..."
	}
	return fmt.Sprintf("--note \"approved: %s\"", goal)
}
