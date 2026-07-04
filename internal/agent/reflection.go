package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// RunStats accumulates observability data during a single RunStreamWithContent
// call. It is the input to the reflection system (the "hill climbing loop"):
// after each run, the stats are analyzed to extract insights that compound
// across sessions.
type RunStats struct {
	// ToolCalls maps tool name to number of invocations.
	ToolCalls map[string]int

	// FilesEdited lists distinct file paths that were written or edited.
	FilesEdited []string

	// CommandsRun lists shell commands executed via run_command or start_command.
	CommandsRun []string

	// Errors records error messages from failed tool calls or stream errors.
	// Truncated to 500 chars each, max 10 entries.
	Errors []string

	// Duration is the wall-clock time from run start to completion.
	Duration time.Duration

	// Iterations is the number of LLM turns in the agent loop.
	Iterations int

	// Success is true if the run completed without error.
	Success bool

	// UserPrompt is the first 200 chars of the user's input, for context.
	UserPrompt string

	// ContextPeakTokens is the highest token count observed during the run.
	// Tracked per-iteration from contextManager.TokenCount().
	ContextPeakTokens int

	// ContextWindow is the model's context window size for this run.
	ContextWindow int

	// CompactionCount is the number of compaction events triggered during the run.
	// Includes both auto-compact and reactive compact.
	CompactionCount int

	// startTime is used internally to compute Duration.
	startTime time.Time
}

// newRunStats creates a fresh RunStats with the start time set.
func newRunStats(userPrompt string) *RunStats {
	return &RunStats{
		ToolCalls:  make(map[string]int),
		UserPrompt: truncatePrompt(userPrompt, 200),
		startTime:  time.Now(),
	}
}

// recordToolCall increments the invocation count for a tool.
func (s *RunStats) recordToolCall(toolName string) {
	if s.ToolCalls == nil {
		s.ToolCalls = make(map[string]int)
	}
	s.ToolCalls[toolName]++
}

// recordFileEdit adds a file path to the edited list (deduplicated).
func (s *RunStats) recordFileEdit(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	for _, existing := range s.FilesEdited {
		if existing == path {
			return
		}
	}
	s.FilesEdited = append(s.FilesEdited, path)
}

// recordCommand adds a shell command to the list (truncated).
func (s *RunStats) recordCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	s.CommandsRun = append(s.CommandsRun, truncatePrompt(cmd, 200))
}

// recordToolError adds a tool execution error for reflection/ratchet rule
// extraction. The format includes the tool name so the LLM can categorize
// the rule correctly. Max 10 entries, each truncated to 500 chars.
func (s *RunStats) recordToolError(toolName, errMsg string) {
	if len(s.Errors) >= 10 {
		return
	}
	msg := fmt.Sprintf("%s: %s", toolName, errMsg)
	s.Errors = append(s.Errors, truncatePrompt(msg, 500))
}

// recordContextUsage tracks peak token usage across iterations.
func (s *RunStats) recordContextUsage(tokens int) {
	if tokens > s.ContextPeakTokens {
		s.ContextPeakTokens = tokens
	}
}

// recordCompaction increments the compaction event counter.
func (s *RunStats) recordCompaction() {
	s.CompactionCount++
}

// totalToolCalls returns the sum of all tool call counts.
func (s *RunStats) totalToolCalls() int {
	total := 0
	for _, n := range s.ToolCalls {
		total += n
	}
	return total
}

// Summary returns a human-readable one-line summary of the run's activity.
// Used when the agent hits max iterations or as a run-completion summary.
func (s *RunStats) Summary() string {
	parts := []string{}
	parts = append(parts, fmt.Sprintf("%d iterations", s.Iterations))
	if tc := s.totalToolCalls(); tc > 0 {
		part := fmt.Sprintf("%d tool calls", tc)
		if len(s.Errors) > 0 {
			part += fmt.Sprintf(" (%d errors)", len(s.Errors))
		}
		parts = append(parts, part)
	}
	if len(s.FilesEdited) > 0 {
		parts = append(parts, fmt.Sprintf("%d files edited", len(s.FilesEdited)))
	}
	if len(s.CommandsRun) > 0 {
		parts = append(parts, fmt.Sprintf("%d commands run", len(s.CommandsRun)))
	}
	if s.Duration > 0 {
		parts = append(parts, formatRunDuration(s.Duration))
	}
	return strings.Join(parts, ", ")
}

func formatRunDuration(d time.Duration) string {
	if d >= time.Minute {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// finalize sets Duration and Success. Iterations is tracked live during the run.
func (s *RunStats) finalize(err error) {
	s.Duration = time.Since(s.startTime)
	s.Success = err == nil
	// Note: agent loop errors (context.Canceled, stream errors) are NOT recorded
	// here — they are ggcode-internal and not actionable for other applications.
	// Only tool execution errors are collected via recordToolError.
}

// ReflectionFunc is called after each RunStreamWithContent completes, with the
// accumulated stats. Implementations may save insights to memory, emit metrics,
// or trigger follow-up actions. The function must be safe to call from a
// goroutine and must not block the agent loop.
type ReflectionFunc func(stats RunStats)

// SetReflectionFunc registers a callback invoked after each run. Pass nil to
// disable. The callback is invoked asynchronously (in a goroutine) to avoid
// blocking the next user interaction.
func (a *Agent) SetReflectionFunc(fn ReflectionFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reflectionFunc = fn
}

// extractPathsFromToolCall parses tool arguments to find file paths and commands.
func extractPathsFromToolCall(toolName string, rawArgs json.RawMessage, s *RunStats) {
	if len(rawArgs) == 0 {
		return
	}
	var args map[string]any
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return
	}
	switch toolName {
	case "write_file", "edit_file", "multi_edit_file", "read_file":
		if path, ok := args["path"].(string); ok {
			s.recordFileEdit(path)
		}
		if path, ok := args["file_path"].(string); ok {
			s.recordFileEdit(path)
		}
	case "run_command", "start_command":
		if cmd, ok := args["command"].(string); ok {
			s.recordCommand(cmd)
		}
	}
}

// GenerateInsights produces a human-readable summary of the run stats suitable
// for saving to project memory. Returns empty string if the run is too trivial
// to warrant a memory entry (e.g., no tools were called).
func GenerateInsights(stats RunStats) string {
	if len(stats.ToolCalls) == 0 && len(stats.FilesEdited) == 0 && len(stats.CommandsRun) == 0 {
		return ""
	}

	var b strings.Builder

	status := "completed"
	if !stats.Success {
		status = "failed"
	}
	fmt.Fprintf(&b, "## Run Reflection (%s, %d iterations, %s)\n", status, stats.Iterations, stats.Duration.Round(time.Second))
	if stats.UserPrompt != "" {
		fmt.Fprintf(&b, "Task: %s\n\n", stats.UserPrompt)
	}

	// Tools used (sorted by frequency, descending)
	if len(stats.ToolCalls) > 0 {
		type toolCount struct {
			name  string
			count int
		}
		var tools []toolCount
		for name, count := range stats.ToolCalls {
			tools = append(tools, toolCount{name, count})
		}
		slices.SortFunc(tools, func(a, b toolCount) int {
			if a.count != b.count {
				return b.count - a.count
			}
			return strings.Compare(a.name, b.name)
		})
		b.WriteString("Tools used:\n")
		for _, t := range tools {
			fmt.Fprintf(&b, "- %s (%d calls)\n", t.name, t.count)
		}
		b.WriteString("\n")
	}

	// Files edited (deduplicated, sorted)
	if len(stats.FilesEdited) > 0 {
		files := make([]string, len(stats.FilesEdited))
		copy(files, stats.FilesEdited)
		slices.Sort(files)
		b.WriteString("Files modified:\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\n")
	}

	// Build commands (deduplicated)
	buildCmds := extractBuildCommands(stats.CommandsRun)
	if len(buildCmds) > 0 {
		b.WriteString("Build/test commands used:\n")
		for _, cmd := range buildCmds {
			fmt.Fprintf(&b, "- `%s`\n", cmd)
		}
		b.WriteString("\n")
	}

	// Errors encountered
	if len(stats.Errors) > 0 {
		b.WriteString("Errors encountered:\n")
		for _, e := range stats.Errors {
			fmt.Fprintf(&b, "- %s\n", e)
		}
		b.WriteString("\n")
	}

	// Context window usage
	if stats.ContextPeakTokens > 0 {
		peakPct := 0.0
		if stats.ContextWindow > 0 {
			peakPct = float64(stats.ContextPeakTokens) / float64(stats.ContextWindow) * 100
		}
		b.WriteString("Context usage:\n")
		fmt.Fprintf(&b, "- Peak tokens: %d", stats.ContextPeakTokens)
		if stats.ContextWindow > 0 {
			fmt.Fprintf(&b, " / %d (%.0f%%)", stats.ContextWindow, peakPct)
		}
		b.WriteString("\n")
		if stats.CompactionCount > 0 {
			fmt.Fprintf(&b, "- Compaction events: %d\n", stats.CompactionCount)
		}
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

// extractBuildCommands filters the command list for build/test/lint commands
// that are worth remembering for future sessions. Returns deduplicated list.
func extractBuildCommands(commands []string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, cmd := range commands {
		cmd = stripCommandComment(cmd)
		lower := strings.ToLower(cmd)
		if isBuildCommand(lower) {
			if idx := strings.IndexByte(cmd, '\n'); idx > 0 {
				cmd = cmd[:idx]
			}
			cmd = strings.TrimSpace(cmd)
			if cmd == "" {
				continue
			}
			if _, ok := seen[cmd]; ok {
				continue
			}
			seen[cmd] = struct{}{}
			result = append(result, cmd)
		}
	}
	return result
}

// isBuildCommand returns true if the command looks like a build/test/lint command.
func isBuildCommand(lower string) bool {
	prefixes := []string{
		"go build", "go test", "go vet", "go run", "go fmt", "go mod",
		"make ", "cmake", "cargo ", "npm ", "yarn ", "pnpm ", "npx ",
		"flutter ", "dart ", "gradle", "mvn ", "python ", "pytest", "pip ",
		"bash scripts/", "sh scripts/", "./scripts/",
		"git add", "git commit", "git status", "git diff", "git log",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// stripCommandComment removes the leading "# description\n" comment that
// commands are prefixed with.
func stripCommandComment(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, "# ") {
		if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
			return strings.TrimSpace(cmd[idx+1:])
		}
	}
	return cmd
}

// maybeReflect calls the reflection handler if one is registered. Called after
// RunStreamWithContent completes. Runs in a goroutine to avoid blocking.
func (a *Agent) maybeReflect(stats *RunStats) {
	a.mu.RLock()
	fn := a.reflectionFunc
	a.mu.RUnlock()
	if fn == nil || stats == nil {
		return
	}
	s := *stats // copy to avoid race
	go func() {
		defer func() {
			if r := recover(); r != nil {
				debug.Log("agent", "reflection handler panicked: %v", r)
			}
		}()
		fn(s)
		// Run ratchet: match errors against existing rules, generalize
		// unmatched ones via LLM. This is the harness ratchet — every
		// error becomes a rule that prevents future mistakes.
		a.runRatchet(&s)
	}()
}

func truncatePrompt(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// MergeInsights appends a new run reflection to the existing insights file,
// keeping only the most recent 10 entries to prevent unbounded growth.
// Shared between TUI, daemon, and desktop surfaces.
func MergeInsights(existing, newEntry string) string {
	entries := SplitRunEntries(existing)
	entries = append(entries, newEntry)
	if len(entries) > 10 {
		entries = entries[len(entries)-10:]
	}
	return strings.Join(entries, "\n\n")
}

// SplitRunEntries splits the memory file into individual run reflection blocks.
// Shared between TUI, daemon, and desktop surfaces.
func SplitRunEntries(content string) []string {
	parts := strings.Split(content, "## Run Reflection")
	var entries []string
	for i, part := range parts {
		if i == 0 {
			if strings.TrimSpace(part) != "" {
				entries = append(entries, strings.TrimSpace(part))
			}
			continue
		}
		entry := "## Run Reflection" + part
		entries = append(entries, strings.TrimSpace(entry))
	}
	return entries
}

// ShouldReflect returns true if the run stats warrant a reflection entry.
// Only runs with meaningful work (3+ tool calls, file edits, or commands)
// get reflections.
func ShouldReflect(stats RunStats) bool {
	totalToolCalls := 0
	for _, count := range stats.ToolCalls {
		totalToolCalls += count
	}
	if totalToolCalls < 3 && len(stats.FilesEdited) == 0 && len(stats.CommandsRun) == 0 {
		return false
	}
	if !stats.Success && stats.Iterations <= 1 {
		return false
	}
	return true
}
