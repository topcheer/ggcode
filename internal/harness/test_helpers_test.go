package harness

import (
	"context"
	"strings"
	"time"
)

// This file holds test-only convenience wrappers referenced by the harness
// test suite. They are kept here (rather than in production code) because no
// production caller uses them; promoting them later is straightforward.

// RunTask runs a goal with default options. It is a thin convenience wrapper
// around RunTaskWithOptions for tests that do not need to customize options.
func RunTask(ctx context.Context, project Project, cfg *Config, goal string, runner Runner) (*RunSummary, error) {
	return RunTaskWithOptions(ctx, project, cfg, goal, runner, RunTaskOptions{})
}

// RerunTask reruns a task by ID with default options. It is a thin convenience
// wrapper around RerunTaskWithOptions for tests that do not need to customize
// options.
func RerunTask(ctx context.Context, project Project, cfg *Config, taskID string, runner Runner) (*RunSummary, error) {
	return RerunTaskWithOptions(ctx, project, cfg, taskID, runner, RunTaskOptions{})
}

// parseMonitorTime parses an RFC3339 timestamp, returning the zero time on
// empty input or parse error. Mirrors the monitor display's time parsing.
func parseMonitorTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
