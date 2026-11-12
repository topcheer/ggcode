package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	assistantTextMaxBytes = 2048
	assistantTextMaxTurns = 40
)

type RunStats struct {
	ToolCalls map[string]int

	FilesEdited []string

	CommandsRun []string

	EditContents []string

	SuccessfulCommands []string

	Errors []string

	ErrorCount int

	Duration time.Duration

	Iterations int

	Success bool

	UserPrompt string

	UserPromptFull string `json:"-"`

	ContextPeakTokens int

	ContextWindow int

	CompactionCount int

	TotalTokens int

	// WaitToolNanos accumulates wall time spent in pure wait tools (sleep,
	// wait_agent, wait_command). That time is idle-by-design: it must not
	// count toward run-duration perf baselines (#5523) or the regression
	// banner reports phantom multi-x slowdowns for rounds that simply
	// waited on a sub-agent or a rate-limit sleep.
	WaitToolNanos int64

	startTime time.Time

	assistantTexts []string

	runID string
}

func newRunStats(userPrompt string) *RunStats {
	return &RunStats{
		ToolCalls:      make(map[string]int),
		UserPrompt:     truncatePrompt(userPrompt, 200),
		UserPromptFull: userPrompt,
		startTime:      time.Now(),
		runID:          generateRunID(),
	}
}

func (s *RunStats) RunID() string { return s.runID }

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

func (s *RunStats) AssistantCorpus() string {
	return strings.Join(s.assistantTexts, "\n")
}

func generateRunID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *RunStats) recordToolCall(toolName string) {
	if s.ToolCalls == nil {
		s.ToolCalls = make(map[string]int)
	}
	s.ToolCalls[toolName]++
}

// waitToolNames are tools whose entire execution window is deliberate
// waiting, not tool work.
var waitToolNames = map[string]bool{
	"sleep":        true,
	"wait_agent":   true,
	"wait_command": true,
}

func isWaitToolName(name string) bool { return waitToolNames[name] }

// AddWaitToolDuration accumulates a wait-tool execution window.
func (s *RunStats) AddWaitToolDuration(d time.Duration) {
	if d > 0 {
		atomic.AddInt64(&s.WaitToolNanos, int64(d))
	}
}

// ActiveDuration is wall duration minus deliberate wait windows: the
// tool-work time the perf baseline should compare across runs (#5523).
func (s *RunStats) ActiveDuration() time.Duration {
	d := s.Duration - time.Duration(atomic.LoadInt64(&s.WaitToolNanos))
	if d < 0 {
		return 0
	}
	return d
}

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

func (s *RunStats) recordToolError(toolName, errMsg string) {

	s.ErrorCount++
	if len(s.Errors) >= 10 {
		return
	}
	msg := fmt.Sprintf("%s: %s", toolName, errMsg)
	s.Errors = append(s.Errors, truncatePrompt(msg, 500))
}

func (s *RunStats) recordTokens(input, output int) {
	s.TotalTokens += input + output
}

func (s *RunStats) recordContextUsage(tokens int) {
	if tokens > s.ContextPeakTokens {
		s.ContextPeakTokens = tokens
	}
}

func (s *RunStats) recordCompaction() {
	s.CompactionCount++
}

func (s *RunStats) totalToolCalls() int {
	total := 0
	for _, n := range s.ToolCalls {
		total += n
	}
	return total
}

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

func (s *RunStats) finalize(err error) {
	s.Duration = time.Since(s.startTime)
	s.Success = err == nil

}

type ReflectionFunc func(stats RunStats)

func (a *Agent) SetReflectionFunc(fn ReflectionFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reflectionFunc = fn
}

func extractPathsFromToolCall(toolName string, rawArgs json.RawMessage, s *RunStats) {
	if len(rawArgs) == 0 {
		return
	}
	var args map[string]any
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return
	}
	args = normalizeApplyPatchArgs(toolName, args) // #5321: nested apply_patch edits count as run edits
	switch toolName {
	case "write_file", "edit_file", "multi_edit_file":

		if path, ok := args["path"].(string); ok {
			s.recordFileEdit(path)
		}
		if path, ok := args["file_path"].(string); ok {
			s.recordFileEdit(path)
		}

		if t, ok := args["new_text"].(string); ok {
			s.recordEditContent(t)
		}
		if t, ok := args["content"].(string); ok {
			s.recordEditContent(t)
		}
	case "apply_patch": // #5321: normalized above; run stats see the edit path
		if path, ok := args["path"].(string); ok {
			s.recordFileEdit(path)
		}
	case "multi_file_edit", "multi_file_write":

		if files, ok := args["files"].([]any); ok {
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if path, ok := fm["path"].(string); ok {
						s.recordFileEdit(path)
					}

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
	case "file_ops":

		recordFileOpsPaths(args, s)
	case "batch_replace":

		recordBatchReplacePaths(args, s)
	case "lsp_rename":

		if path, ok := args["path"].(string); ok {
			s.recordFileEdit(path)
		}
	}
}

func recordFileOpsPaths(args map[string]any, s *RunStats) {
	ops, ok := args["operations"].([]any)
	if !ok {
		return
	}
	for _, op := range ops {
		om, ok := op.(map[string]any)
		if !ok {
			continue
		}
		if p, ok := om["source"].(string); ok && p != "" {
			s.recordFileEdit(p)
		}
		if p, ok := om["destination"].(string); ok && p != "" {
			s.recordFileEdit(p)
		}
	}
}

func recordBatchReplacePaths(args map[string]any, s *RunStats) {
	files, ok := args["files"].([]any)
	if !ok {
		return
	}
	for _, f := range files {
		if p, ok := f.(string); ok && p != "" {
			s.recordFileEdit(p)
		}
	}
}

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

	if !stats.Success {
		b.WriteString("[verified: no - this run FAILED; the observations below are unverified post-mortem notes, not proven practices]\n")
	}
	if stats.UserPrompt != "" {
		// #5118: same redaction as the training-export pipeline - run
		// insights persist to project auto-memory, raw secrets must not.
		fmt.Fprintf(&b, "Task: %s\n\n", redactTrainText(stats.UserPrompt))
	}

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

	buildCmds := extractBuildCommands(stats.CommandsRun)
	if len(buildCmds) > 0 {
		b.WriteString("Build/test commands used:\n")
		for _, cmd := range buildCmds {
			fmt.Fprintf(&b, "- `%s`\n", cmd)
		}
		b.WriteString("\n")
	}

	if len(stats.Errors) > 0 {
		b.WriteString("Errors encountered:\n")
		for _, e := range stats.Errors {
			fmt.Fprintf(&b, "- %s\n", e)
		}
		b.WriteString("\n")
	}

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

func stripCommandComment(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, "# ") {
		if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
			return strings.TrimSpace(cmd[idx+1:])
		}
	}
	return cmd
}

func (a *Agent) maybeReflect(stats *RunStats) {
	a.mu.RLock()
	fn := a.reflectionFunc
	a.mu.RUnlock()
	if fn == nil || stats == nil {
		return
	}
	s := *stats

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

		a.qualityScorer.maybeDetectRegression()
	}

	safego.Go("agent.reflection", func() {
		defer func() {
			if r := recover(); r != nil {
				debug.Log("agent", "reflection handler panicked: %v", r)
			}
		}()
		fn(s)

		a.recordPlaybook(&s)

		a.runRatchet(&s)
	})
}

func truncatePrompt(s string, maxLen int) string {
	return util.Truncate(s, maxLen)
}

func MergeInsights(existing, newEntry string) string {
	entries := SplitRunEntries(existing)
	entries = append(entries, newEntry)
	if len(entries) > 10 {
		entries = entries[len(entries)-10:]
	}
	return strings.Join(entries, "\n\n")
}

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
