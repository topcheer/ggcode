package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

// assistantTextMaxBytes / assistantTextMaxTurns (sa-147) cap the
// consumption-scanning corpus recorded per run: memory fingerprints
// (keys, first content lines) live early in a turn; reflection needs
// evidence, not fidelity.
const (
	assistantTextMaxBytes = 2048
	assistantTextMaxTurns = 40
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

	// EditContents lists the ADDED text of edit-family tool calls
	// (write_file content / edit_file+multi_* new_text / notebook new
	// source): the write-vector counterpart of CommandsRun. Consumed by
	// spec-gaming Pattern 2 (#3696) so injecting a test skip marker via the
	// agent's primary edit tools cannot silently bypass the detector the
	// way it could when only shell commands were scanned. Only ADDED text
	// is kept (old_text is removal, not gaming); entries truncated and
	// capped like CommandsRun.
	EditContents []string

	// SuccessfulCommands lists commands that completed successfully
	// (non-error result), deduplicated and capped. Consumed by the
	// trajectory→asset distiller to persist verified commands as
	// cmd_snippet entries.
	SuccessfulCommands []string

	// Errors records error messages from failed tool calls or stream errors.
	// Truncated to 500 chars each, max 10 entries.
	Errors []string

	// ErrorCount is the TOTAL number of recorded errors, unaffected by
	// the 10-entry Errors cap (#1490-A). The cap exists to bound the
	// reflection prompt; scorers reading len(Errors) systematically
	// underestimated error-heavy runs (22 failures in 25 calls read as
	// 10/25). Scoring paths must use ErrorCount; prompt paths keep
	// using Errors.
	ErrorCount int

	// Duration is the wall-clock time from run start to completion.
	Duration time.Duration

	// Iterations is the number of LLM turns in the agent loop.
	Iterations int

	// Success is true if the run completed without error.
	Success bool

	// UserPrompt is the first 200 chars of the user's input, for context.
	UserPrompt string

	// UserPromptFull is the complete, untruncated user input (r414). It is
	// NOT serialized to the run journal (the 200-char form is the persisted
	// context record). Consumers that must not lose late-position content -
	// e.g. preference distillation, where a durable "from now on ..."
	// statement routinely follows a pasted log or code block - read this
	// field instead of UserPrompt.
	UserPromptFull string `json:"-"`

	// ContextPeakTokens is the highest token count observed during the run.
	// Tracked per-iteration from contextManager.TokenCount().
	ContextPeakTokens int

	// ContextWindow is the model's context window size for this run.
	ContextWindow int

	// CompactionCount is the number of compaction events triggered during the run.
	// Includes both auto-compact and reactive compact.
	CompactionCount int

	// TotalTokens accumulates input+output tokens across all LLM calls this
	// run (r394: cost dimension of the perf baseline). Prompt bloat, fallback
	// chains switching to pricier models, and cache misses show up here
	// before they show in duration.
	TotalTokens int

	// startTime is used internally to compute Duration.
	startTime time.Time

	// assistantTexts (sa-147, LIMBO inference-time memory allocation)
	// holds capped assistant text turns for post-run consumption scanning
	// (memory key / fingerprint matching, memory/consumption.go). Unexported;\t	// exposed via AssistantCorpus.
	assistantTexts []string

	// runID is a unique identifier for this run, used for checkpoint
	// run-boundary tracking (UndoRun). Generated at creation time.
	runID string
}

// newRunStats creates a fresh RunStats with the start time set.
func newRunStats(userPrompt string) *RunStats {
	return &RunStats{
		ToolCalls:      make(map[string]int),
		UserPrompt:     truncatePrompt(userPrompt, 200),
		UserPromptFull: userPrompt,
		startTime:      time.Now(),
		runID:          generateRunID(),
	}
}

// RunID returns the unique run identifier.
func (s *RunStats) RunID() string { return s.runID }

// recordAssistantText (sa-147) appends one assistant turn's text for
// post-run memory-consumption scanning. Capped: last 40 turns, first 2KB
// each - consumption fingerprints (memory keys, first content lines) live
// early in a turn, and reflection only needs evidence, not fidelity.
func (s *RunStats) recordAssistantText(text string) {
	if text == "" {
		return
	}
	if len(text) > assistantTextMaxBytes {
		text = text[:assistantTextMaxBytes]
	}
	s.assistantTexts = append(s.assistantTexts, text)
	if len(s.assistantTexts) > assistantTextMaxTurns {
		s.assistantTexts = s.assistantTexts[len(s.assistantTexts)-40:]
	}
}

// AssistantCorpus returns the capped concatenation of recorded assistant
// texts ("" when none) for memory consumption scanning.
func (s *RunStats) AssistantCorpus() string {
	return strings.Join(s.assistantTexts, "\n")
}

// generateRunID produces a short hex string for run identification.
func generateRunID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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

// recordSuccessfulCommand adds a verified-successful shell command
// (non-error tool result) for trajectory→asset distillation.
// Deduplicated, max 30 entries, each truncated to 500 chars.
func (s *RunStats) recordSuccessfulCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	for _, existing := range s.SuccessfulCommands {
		if existing == cmd {
			return
		}
	}
	if len(s.SuccessfulCommands) >= 30 {
		return
	}
	s.SuccessfulCommands = append(s.SuccessfulCommands, truncatePrompt(cmd, 500))
}

// recordCommand adds a shell command to the list (truncated).
// Max 30 entries, each truncated to 200 chars. Prevents unbounded
// growth in long autopilot/cron sessions.
func (s *RunStats) recordCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	if len(s.CommandsRun) >= 30 {
		return
	}
	s.CommandsRun = append(s.CommandsRun, truncatePrompt(cmd, 200))
}

// recordEditContent adds one edit-family tool call's added text to
// EditContents (#3696). Max 30 entries, each truncated to 4KB - skip
// markers are short but sit anywhere in a large new_text, so the truncat
// budget is deliberately larger than recordCommand's 200.
func (s *RunStats) recordEditContent(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if len(s.EditContents) >= 30 {
		return
	}
	s.EditContents = append(s.EditContents, truncatePrompt(text, 4096))
}

// recordToolError adds a tool execution error for reflection/ratchet rule
// extraction. The format includes the tool name so the LLM can categorize
// the rule correctly. Max 10 entries, each truncated to 500 chars.
func (s *RunStats) recordToolError(toolName, errMsg string) {
	// #1490-A: count EVERY error; the 10-entry list only bounds the
	// reflection prompt. Scorers reading len(Errors) underestimated
	// error-heavy runs (22 failures in 25 calls read as 10/25).
	s.ErrorCount++
	if len(s.Errors) >= 10 {
		return
	}
	msg := fmt.Sprintf("%s: %s", toolName, errMsg)
	s.Errors = append(s.Errors, truncatePrompt(msg, 500))
}

// recordTokens accumulates total input+output tokens for the run (r394
// perf-baseline cost dimension).
func (s *RunStats) recordTokens(input, output int) {
	s.TotalTokens += input + output
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
			part += fmt.Sprintf(" (%d errors)", s.ErrorCount)
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
	case "write_file", "edit_file", "multi_edit_file":
		// NOTE: read_file is intentionally excluded — FilesEdited must only
		// contain files that were actually modified. Including read_file here
		// caused false-positive warnings from post-completion gates
		// (complexity_gate, companion_guard, change_reconcile) on turns
		// where the agent only read files without editing them.
		if path, ok := args["path"].(string); ok {
			s.recordFileEdit(path)
		}
		if path, ok := args["file_path"].(string); ok {
			s.recordFileEdit(path)
		}
		// #3696: capture the ADDED text so spec-gaming Pattern 2 can scan
		// edit-injected skip markers, not just shell-injected ones.
		if t, ok := args["new_text"].(string); ok {
			s.recordEditContent(t)
		}
		if t, ok := args["content"].(string); ok {
			s.recordEditContent(t)
		}
	case "multi_file_edit", "multi_file_write":
		// {"files": [{"path": "...", ...}, ...]}
		if files, ok := args["files"].([]any); ok {
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if path, ok := fm["path"].(string); ok {
						s.recordFileEdit(path)
					}
					// #3696: same added-text capture for batched edits.
					if t, ok := fm["new_text"].(string); ok {
						s.recordEditContent(t)
					}
					if t, ok := fm["content"].(string); ok {
						s.recordEditContent(t)
					}
				}
			}
		}
	case "notebook_edit":
		if path, ok := args["notebook_path"].(string); ok {
			s.recordFileEdit(path)
		}
		// #3696: notebook cells carry source as a list of lines; a skip
		// marker can be injected on any line.
		if srcs, ok := args["new_source"].([]any); ok {
			var b strings.Builder
			for _, ln := range srcs {
				if l, ok := ln.(string); ok {
					b.WriteString(l)
					b.WriteByte('\n')
				}
			}
			s.recordEditContent(b.String())
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
	// sa-139 (MemGuard): run-insights are injected into every future prompt,
	// so the verification signal must travel WITH the text, not only in the
	// sidecar. Observations distilled from a failed run are unverified - say
	// so up front so later runs do not treat them as proven practices.
	if !stats.Success {
		b.WriteString("[verified: no - this run FAILED; the observations below are unverified post-mortem notes, not proven practices]\n")
	}
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

	// Score run quality for provider/model A/B comparison.
	if a.qualityScorer != nil {
		providerName := ""
		modelName := ""
		if p, ok := a.provider.(interface{ Name() string }); ok {
			providerName = p.Name()
		}
		if m, ok := a.provider.(interface{ ModelName() string }); ok {
			modelName = m.ModelName()
		}
		a.qualityScorer.ScoreRun(&s, providerName, modelName)
		// Detect quality regression against the rolling historical baseline
		// (Eval-Driven Development: catch quality degradation early).
		a.qualityScorer.maybeDetectRegression()
	}

	safego.Go("agent.reflection", func() {
		defer func() {
			if r := recover(); r != nil {
				debug.Log("agent", "reflection handler panicked: %v", r)
			}
		}()
		fn(s)
		// Record run outcomes in the playbook (ACE-inspired): successes
		// create/upgrade entries, failures degrade SuccessRate so prune can
		// evict degraded strategies (#3557). Complements ratchet's learning
		// from errors.
		a.recordPlaybook(&s)
		// Run ratchet: match errors against existing rules, generalize
		// unmatched ones via LLM. This is the learning ratchet — every
		// error becomes a rule that prevents future mistakes.
		a.runRatchet(&s)
	})
}

func truncatePrompt(s string, maxLen int) string {
	return util.Truncate(s, maxLen)
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
