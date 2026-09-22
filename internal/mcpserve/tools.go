package mcpserve

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// RunRequest is the argument contract of the ggcode_run tool.
type RunRequest struct {
	// Prompt is the self-contained task for the headless agent run.
	Prompt string `json:"prompt"`
	// WorkingDir is the directory the child process runs in. Empty means the
	// server's own working directory.
	WorkingDir string `json:"working_dir"`
	// TimeoutSeconds overrides the server default timeout for this call.
	// Values above the server cap are clamped.
	TimeoutSeconds int `json:"timeout_seconds"`
}

// RunResult is the outcome of a headless run.
type RunResult struct {
	// Output is the agent's final answer text.
	Output string `json:"output"`
}

// RunTool executes one headless ggcode run. Implemented by execRunner;
// tests inject fakes.
type RunTool interface {
	Run(ctx context.Context, req RunRequest) (RunResult, error)
}

// SessionSummary is one entry of the session list.
type SessionSummary struct {
	ID        string
	Title     string
	Preview   string
	Workspace string
	Model     string
	UpdatedAt time.Time
}

// SessionSource backs the session tools. Implemented by jsonlSessionSource;
// tests inject fakes or real stores.
type SessionSource interface {
	List(limit int) ([]SessionSummary, error)
	Read(id string, maxMessages int) (string, error)
}

// execRunner executes ggcode in headless pipe mode as a child process
// (`ggcode -p <prompt> --output <tmpfile> --config <cfg>`). Process isolation
// mirrors how Claude Code's mcp serve delegates to headless mode: no in-process
// agent state is shared, the child uses the user's normal config, and a crash
// cannot take the server down. Stderr is captured for error reporting.
type execRunner struct {
	exe     string // ggcode binary; empty → os.Executable()
	cfgPath string // forwarded via --config; may be empty
	// execCommand builds the child command; overridable in tests.
	execCommand func(ctx context.Context, exe string, args []string, dir string, env []string) *exec.Cmd
}

// newExecRunner builds the default runner.
func newExecRunner(exe, cfgPath string, _ time.Duration) *execRunner {
	return &execRunner{exe: exe, cfgPath: cfgPath}
}

// buildRunArgs constructs the child process arguments. Extracted for testing.
func buildRunArgs(prompt, outPath, cfgPath string) []string {
	args := []string{"-p", prompt, "--output", outPath}
	if cfgPath != "" {
		args = append(args, "--config", cfgPath)
	}
	return args
}

// normalizeWorkingDir resolves the child working directory. Empty → current
// directory; relative → absolute (and must exist).
func normalizeWorkingDir(dir string) (string, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return os.Getwd()
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolving working_dir: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("working_dir %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working_dir %s is not a directory", abs)
	}
	return abs, nil
}

// Run performs one headless run. The child writes its answer to a temp file
// (--output) so we capture only the final text, never interleaved progress;
// progress goes to the child's stderr, which is buffered and only surfaced on
// failure.
func (r *execRunner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	dir, err := normalizeWorkingDir(req.WorkingDir)
	if err != nil {
		return RunResult{}, err
	}
	exe := r.exe
	if exe == "" {
		exe, err = os.Executable()
		if err != nil {
			return RunResult{}, fmt.Errorf("resolving ggcode executable: %w", err)
		}
	}
	outFile, err := os.CreateTemp("", "ggcode-mcp-run-*.txt")
	if err != nil {
		return RunResult{}, fmt.Errorf("creating output temp file: %w", err)
	}
	outPath := outFile.Name()
	if err := outFile.Close(); err != nil {
		return RunResult{}, fmt.Errorf("creating output temp file: %w", err)
	}
	defer func() {
		if err := os.Remove(outPath); err != nil && !os.IsNotExist(err) {
			debug.Log("mcpserve", "removing temp output: %v", err)
		}
	}()

	args := buildRunArgs(req.Prompt, outPath, r.cfgPath)
	build := r.execCommand
	if build == nil {
		build = func(ctx context.Context, exe string, args []string, dir string, env []string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, exe, args...)
			cmd.Dir = dir
			if len(env) > 0 {
				cmd.Env = env
			}
			return cmd
		}
	}
	cmd := build(ctx, exe, args, dir, os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	debug.Log("mcpserve", "ggcode_run exec: dir=%s prompt=%s", dir, truncate(req.Prompt, 120))
	if err := cmd.Run(); err != nil {
		stderrTail := tailBytes(stderr.String(), 2000)
		if ctx.Err() != nil {
			return RunResult{}, fmt.Errorf("ggcode run timed out or was cancelled: %w; stderr: %s", ctx.Err(), stderrTail)
		}
		return RunResult{}, fmt.Errorf("ggcode run failed: %w; stderr: %s", err, stderrTail)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return RunResult{}, fmt.Errorf("reading run output: %w", err)
	}
	return RunResult{Output: string(data)}, nil
}

// jsonlSessionSource adapts the local JSONL session store to SessionSource.
type jsonlSessionSource struct {
	open func() (*session.JSONLStore, error)
}

// newJSONLSessionSource uses the default session directory.
func newJSONLSessionSource() *jsonlSessionSource {
	return &jsonlSessionSource{
		open: func() (*session.JSONLStore, error) {
			return session.NewDefaultStore()
		},
	}
}

// List returns up to limit recent sessions.
func (s *jsonlSessionSource) List(limit int) ([]SessionSummary, error) {
	store, err := s.open()
	if err != nil {
		return nil, err
	}
	sessions, err := store.List()
	if err != nil {
		return nil, err
	}
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}
	out := make([]SessionSummary, 0, len(sessions))
	for _, ses := range sessions {
		out = append(out, SessionSummary{
			ID:        ses.ID,
			Title:     ses.Title,
			Preview:   ses.Preview,
			Workspace: ses.Workspace,
			Model:     ses.Model,
			UpdatedAt: ses.UpdatedAt,
		})
	}
	return out, nil
}

// Read loads a session and formats its most recent messages as a transcript.
func (s *jsonlSessionSource) Read(id string, maxMessages int) (string, error) {
	store, err := s.open()
	if err != nil {
		return "", err
	}
	// Full load: the tail window is all this tool needs, and headers-only
	// loads skip message records entirely.
	ses, err := store.LoadWithOptions(id, true)
	if err != nil {
		return "", err
	}
	return formatTranscript(ses, maxMessages), nil
}

// maxMessageTextRunes bounds each message's text in the transcript so one
// giant tool result cannot blow up the caller's context.
const maxMessageTextRunes = 2000

// formatTranscript renders the last maxMessages messages as role-tagged text.
func formatTranscript(ses *session.Session, maxMessages int) string {
	if ses == nil {
		return "(empty session)"
	}
	msgs := ses.Messages
	start := 0
	if len(msgs) > maxMessages {
		start = len(msgs) - maxMessages
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Session %s | %s | messages=%d (showing last %d)\n",
		ses.ID, ses.Title, len(msgs), len(msgs)-start)
	for i, msg := range msgs[start:] {
		role := msg.Role
		texts := collectTexts(msg.Content)
		if len(texts) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "[%d] %s: ", i+1, role)
		for j, t := range texts {
			if j > 0 {
				sb.WriteString(" | ")
			}
			sb.WriteString(truncateRunes(t, maxMessageTextRunes))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// collectTexts extracts text payloads from content blocks.
func collectTexts(blocks []provider.ContentBlock) []string {
	var out []string
	for _, b := range blocks {
		if strings.TrimSpace(b.Text) == "" {
			continue
		}
		out = append(out, b.Text)
	}
	return out
}

// truncateRunes cuts s to at most n runes, appending an ellipsis marker.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…[truncated]"
}

// truncate cuts s to at most n bytes (used for log lines).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// tailBytes returns the last n bytes of s.
func tailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…(truncated)" + s[len(s)-n:]
}
