package agent

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Cross-Run Behavioral Pattern Detection
//
// Research: Devin, Claude Code, OpenHands, and Aider all increasingly track
// agent behavior analytics. The key insight from 2025-2026 agent research is
// that individual-run reflection (learning from a single run's mistakes) is
// insufficient - agents also need CROSS-RUN pattern detection to identify
// SYSTEMIC behavioral anti-patterns that persist across multiple sessions.
//
// Competitor analysis:
//   - Devin: tracks agent "performance score" across tasks, uses it to route
//     tasks and flag areas needing improvement. Proprietary, not visible to users.
//   - Claude Code: no cross-run behavioral analytics. Each run is independent.
//   - Cursor: tracks accept/reject rates per suggestion but only for inline
//     completions, not for agentic coding sessions.
//   - OpenHands: has per-run analytics but no cross-run pattern detection.
//   - Aider: no behavioral analytics at all.
//
// ggcode already has extensive per-run reflection (maybeReflect, GenerateInsights,
// playbook, ratchet). But it has NO mechanism to detect patterns that only
// emerge across multiple runs:
//   - "You haven't run a single build/test in your last 4 coding runs"
//   - "Your edit failure rate has been > 40% across the last 5 runs"
//   - "You consistently use 15+ iterations per run (median: 17)"
//
// This tracker maintains a ring buffer of recent RunStats (surviving across
// runs within the same agent instance) and analyzes them at the start of each
// new run. When a systemic pattern is detected, it injects a proactive
// guidance message to course-correct the agent's behavior.

const behaviorPatternWindowSize = 5 // number of recent runs to analyze

// behaviorPatternState tracks recent run statistics to detect systemic patterns.
type behaviorPatternState struct {
	mu          sync.Mutex
	recent      []RunStatsSnapshot
	injectedFor int // how many times we've injected this session (rate limit)
}

// RunStatsSnapshot is a lightweight copy of RunStats fields relevant to
// cross-run pattern analysis. Kept minimal to avoid retaining large data
// (error messages, command lists, etc.) across runs.
type RunStatsSnapshot struct {
	Iterations   int
	ToolCalls    int
	FilesEdited  int
	Errors       int
	CommandsRun  int
	Duration     time.Duration
	HadBuildTest bool // whether any build/test/lint command was run
	HadFileEdits bool // whether any files were edited
	EditFailures int  // edit tool calls that resulted in errors
	EditAttempts int  // total edit tool calls (edit_file, write_file, etc.)
	Success      bool
	Timestamp    time.Time
}

func newBehaviorPatternState() *behaviorPatternState {
	return &behaviorPatternState{
		recent: make([]RunStatsSnapshot, 0, behaviorPatternWindowSize+1),
	}
}

// recordRun adds a completed run's stats to the ring buffer.
func (b *behaviorPatternState) recordRun(stats *RunStats) {
	if b == nil || stats == nil {
		return
	}

	snap := snapshotFromRunStats(stats)

	b.mu.Lock()
	defer b.mu.Unlock()
	b.recent = append(b.recent, snap)
	if len(b.recent) > behaviorPatternWindowSize {
		b.recent = b.recent[len(b.recent)-behaviorPatternWindowSize:]
	}
}

// snapshotFromRunStats converts RunStats to a compact snapshot.
func snapshotFromRunStats(stats *RunStats) RunStatsSnapshot {
	snap := RunStatsSnapshot{
		Iterations:   stats.Iterations,
		FilesEdited:  len(stats.FilesEdited),
		Errors:       len(stats.Errors),
		CommandsRun:  len(stats.CommandsRun),
		Duration:     stats.Duration,
		HadFileEdits: len(stats.FilesEdited) > 0,
		Success:      stats.Success,
		Timestamp:    time.Now(),
	}

	// Count total tool calls and analyze edit tools
	totalCalls := 0
	editAttempts := 0
	editFailures := 0
	for toolName, count := range stats.ToolCalls {
		totalCalls += count
		if isEditTool(toolName) {
			editAttempts += count
		}
	}
	snap.ToolCalls = totalCalls
	snap.EditAttempts = editAttempts

	// Detect edit failures from errors
	for _, errMsg := range stats.Errors {
		if isEditError(errMsg) {
			editFailures++
		}
	}
	snap.EditFailures = editFailures

	// Detect build/test commands
	for _, cmd := range stats.CommandsRun {
		lower := strings.ToLower(stripCommandComment(cmd))
		if isBuildTestCommand(lower) {
			snap.HadBuildTest = true
			break
		}
	}

	return snap
}

func isEditError(errMsg string) bool {
	// Errors from edit tools are prefixed with tool name
	editTools := []string{"edit_file:", "write_file:", "multi_edit_file:", "multi_file_edit:", "multi_file_write:"}
	for _, t := range editTools {
		if strings.HasPrefix(errMsg, t) {
			return true
		}
	}
	return false
}

func isBuildTestCommand(lower string) bool {
	prefixes := []string{
		"go build", "go test", "go vet",
		"make ", "cargo ", "cmake",
		"npm run", "yarn ", "pnpm ", "npx ",
		"flutter ", "dart ", "gradle", "mvn ",
		"pytest", "python -m pytest", "python -m unittest",
		"./scripts/", "bash scripts/", "sh scripts/",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	// Direct binary test runners
	if strings.Contains(lower, "pytest") || strings.Contains(lower, "jest") || strings.Contains(lower, "vitest") {
		return true
	}
	return false
}

// BehaviorPattern represents a detected systemic anti-pattern.
type BehaviorPattern struct {
	Type        string
	Severity    string // "high" or "medium"
	Description string
	Guidance    string
}

// detectPatterns analyzes the recent run snapshots and returns any detected
// systemic patterns. Returns nil if no patterns are found or insufficient data.
func (b *behaviorPatternState) detectPatterns() []BehaviorPattern {
	if b == nil {
		return nil
	}

	b.mu.Lock()
	runs := make([]RunStatsSnapshot, len(b.recent))
	copy(runs, b.recent)
	b.mu.Unlock()

	// Need at least 3 runs to detect a pattern
	if len(runs) < 3 {
		return nil
	}

	var patterns []BehaviorPattern

	// Pattern 1: Chronic verification skipping
	// The agent edited files in multiple recent runs but never ran a build/test
	editRunsNoVerify := 0
	for _, r := range runs {
		if r.HadFileEdits && !r.HadBuildTest {
			editRunsNoVerify++
		}
	}
	if editRunsNoVerify >= 3 {
		patterns = append(patterns, BehaviorPattern{
			Type:        "verification_skip",
			Severity:    "high",
			Description: fmt.Sprintf("No build/test/lint run in %d of %d recent editing sessions", editRunsNoVerify, len(runs)),
			Guidance:    fmt.Sprintf("Behavioral pattern detected: you have edited files in %d recent runs without running any build or test verification. This is a systemic anti-pattern. Make sure to run the project's build and test commands after making code changes to catch errors early.", editRunsNoVerify),
		})
	}

	// Pattern 2: High edit failure rate across runs
	// Significant proportion of edit tool calls result in errors
	totalEditAttempts := 0
	totalEditFailures := 0
	for _, r := range runs {
		totalEditAttempts += r.EditAttempts
		totalEditFailures += r.EditFailures
	}
	if totalEditAttempts >= 6 && totalEditFailures > 0 {
		failRate := float64(totalEditFailures) / float64(totalEditAttempts)
		if failRate >= 0.40 {
			patterns = append(patterns, BehaviorPattern{
				Type:        "high_edit_failure",
				Severity:    "medium",
				Description: fmt.Sprintf("Edit failure rate %.0f%% (%d/%d) across recent runs", failRate*100, totalEditFailures, totalEditAttempts),
				Guidance:    fmt.Sprintf("Behavioral pattern detected: your edit failure rate is %.0f%% (%d failures out of %d edit attempts across recent runs). Always read a file immediately before editing it, use exact line content from read_file as anchors, and verify old_text uniqueness before submitting.", failRate*100, totalEditFailures, totalEditAttempts),
			})
		}
	}

	// Pattern 3: Chronic high iteration count
	// Multiple runs consuming excessive iterations (>15 each)
	highIterRuns := 0
	for _, r := range runs {
		if r.Iterations > 15 {
			highIterRuns++
		}
	}
	if highIterRuns >= 3 {
		patterns = append(patterns, BehaviorPattern{
			Type:        "high_iteration_count",
			Severity:    "medium",
			Description: fmt.Sprintf("%d of %d recent runs used >15 iterations", highIterRuns, len(runs)),
			Guidance:    fmt.Sprintf("Behavioral pattern detected: %d of your recent runs used more than 15 iterations each. This suggests difficulty converging on solutions. Consider: breaking tasks into smaller steps, planning before acting, and verifying incremental progress rather than making many speculative changes.", highIterRuns),
		})
	}

	// Pattern 4: Consistent run failures
	// Multiple recent runs ended in failure
	failedRuns := 0
	for _, r := range runs {
		if !r.Success {
			failedRuns++
		}
	}
	if failedRuns >= 3 {
		patterns = append(patterns, BehaviorPattern{
			Type:        "chronic_failure",
			Severity:    "high",
			Description: fmt.Sprintf("%d of %d recent runs failed", failedRuns, len(runs)),
			Guidance:    fmt.Sprintf("Behavioral pattern detected: %d of your recent %d runs ended in failure. Before starting complex work, verify your environment and dependencies are correct. Break large tasks into smaller, independently verifiable steps.", failedRuns, len(runs)),
		})
	}

	return patterns
}

// shouldInject returns true if pattern injection hasn't been rate-limited.
// We inject at most once per run to avoid nagging.
func (b *behaviorPatternState) shouldInject() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.injectedFor >= 1 {
		return false
	}
	b.injectedFor++
	return true
}

// reset clears the injection counter for a new run window.
func (b *behaviorPatternState) reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.injectedFor = 0
}

// maybeInjectBehaviorPattern checks for systemic patterns and injects guidance
// at the start of a new run if any are detected. This is the cross-run
// counterpart to per-run correction feedback.
func (a *Agent) maybeInjectBehaviorPattern() {
	if a.behaviorPattern == nil {
		return
	}

	patterns := a.behaviorPattern.detectPatterns()
	if len(patterns) == 0 {
		return
	}

	if !a.behaviorPattern.shouldInject() {
		return
	}

	// Sort by severity: high first
	slices.SortFunc(patterns, func(a, b BehaviorPattern) int {
		if a.Severity == b.Severity {
			return strings.Compare(a.Type, b.Type)
		}
		if a.Severity == "high" {
			return -1
		}
		return 1
	})

	// Build a concise message with the top patterns (max 2 to avoid flooding)
	maxPatterns := 2
	if len(patterns) < maxPatterns {
		maxPatterns = len(patterns)
	}

	var buf strings.Builder
	buf.WriteString("[Cross-Run Behavioral Pattern Analysis]\n")
	buf.WriteString("Analysis of your recent sessions detected the following systemic patterns:\n\n")
	for i := 0; i < maxPatterns; i++ {
		fmt.Fprintf(&buf, "%d. %s\n   %s\n\n", i+1, patterns[i].Description, patterns[i].Guidance)
	}
	buf.WriteString("Address these patterns in your current session to improve reliability.\n")

	msg := buf.String()
	debug.Log("agent", "behavior pattern: injecting %d detected patterns", len(patterns))

	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: msg,
		}},
	})
}
