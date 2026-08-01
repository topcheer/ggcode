package agent

// Post-edit auto-format: automatically run language-appropriate formatters
// on files after they are written or edited by the agent.
//
// Competitive landscape:
//   - Aider: Runs goimports/rustfmt/black automatically after edits
//   - Cursor: Format-on-save with language-specific formatters
//   - Cline: Supports configurable format commands
//   - Claude Code: Relies on editor format-on-save, doesn't auto-format
//
// ggcode's approach: deterministic, zero-LLM-cost formatting that runs
// synchronously after each successful file write. Uses the project's
// existing toolchain — no new dependencies. If a formatter is not installed,
// the step is silently skipped (no error, no warning).
//
// Design decisions:
//   - Only runs on files that were actually changed (not cancelled writes)
//   - Skips files larger than formatMaxFileBytes to avoid slow formatting
//   - Timeout of formatTimeout prevents hanging on large/slow files
//   - If formatting changes the file, a brief notice is appended to the
//     tool result so the agent knows the on-disk content may differ from
//     what it wrote
//   - Formatting failures (non-zero exit) are silently ignored — the
//     agent's original content remains on disk

import (
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// formatMaxFileBytes skips formatting files larger than 1MB to avoid
	// slow formatting operations that would block the agent loop.
	formatMaxFileBytes = 1 << 20

	// formatTimeout is the maximum wall-clock time for a formatter.
	formatTimeout = 15 * time.Second
)

// formatCommand describes a formatter for a specific file type.
type formatCommand struct {
	command string   // e.g. "gofmt", "goimports", "prettier"
	args    []string // e.g. ["-w"] for write-in-place
}

// formatterForFile returns the appropriate format command for the given
// file path based on its extension. Returns nil if no formatter is known
// for this file type.
func formatterForFile(path string) *formatCommand {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		// Prefer goimports if available (handles gofmt + import management),
		// fall back to gofmt.
		if path, _ := exec.LookPath("goimports"); path != "" {
			return &formatCommand{command: "goimports", args: []string{"-w", path}}
		}
		return &formatCommand{command: "gofmt", args: []string{"-w"}}
	case ".rs":
		return &formatCommand{command: "rustfmt", args: []string{"--edition", "2021"}}
	case ".py":
		// Prefer ruff (fast, modern), fall back to black.
		if _, err := exec.LookPath("ruff"); err == nil {
			return &formatCommand{command: "ruff", args: []string{"format"}}
		}
		return &formatCommand{command: "black", args: []string{"-q"}}
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts", ".json", ".css", ".scss", ".html", ".vue", ".svelte":
		return &formatCommand{command: "prettier", args: []string{"--write"}}
	case ".c", ".cpp", ".cc", ".h", ".hpp":
		return &formatCommand{command: "clang-format", args: []string{"-i"}}
	case ".sh", ".bash":
		return &formatCommand{command: "shfmt", args: []string{"-w"}}
	case ".dart":
		return &formatCommand{command: "dart", args: []string{"format"}}
	default:
		return nil
	}
}

// autoFormatFile runs the appropriate formatter on the given file path.
// It returns a notice string if formatting changed the file, or "" if
// the file was unchanged, the formatter was not found, or formatting failed.
func autoFormatFile(filePath string) string {
	fc := formatterForFile(filePath)
	if fc == nil {
		return ""
	}

	// Check if the formatter binary exists on the system.
	binPath, err := exec.LookPath(fc.command)
	if err != nil {
		debug.Log("autoformat", "formatter %q not found, skipping %s", fc.command, filePath)
		return ""
	}

	// Read file size to skip large files.
	if info, err := execLookStat(filePath); err != nil || info >= formatMaxFileBytes {
		debug.Log("autoformat", "skipping %s (size=%d, max=%d or stat error)", filePath, info, formatMaxFileBytes)
		return ""
	}

	// Read before content to detect changes.
	before, _ := execLookReadFile(filePath)

	args := append(append([]string{}, fc.args...), filePath)
	cmd := exec.Command(binPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Use a channel + timeout to prevent hanging.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case err := <-done:
		if err != nil {
			debug.Log("autoformat", "formatter %s failed for %s: %v", fc.command, filePath, err)
			return ""
		}
	case <-time.After(formatTimeout):
		_ = execKillProcess(cmd)
		debug.Log("autoformat", "formatter %s timed out for %s", fc.command, filePath)
		return ""
	}

	after, _ := execLookReadFile(filePath)
	if before != after && after != "" {
		debug.Log("autoformat", "formatted %s with %s (%d → %d bytes)", filePath, fc.command, len(before), len(after))
		return "[Auto-format] Applied " + fc.command + " to " + filepath.Base(filePath) + ". Content may differ from what was written."
	}
	return ""
}
