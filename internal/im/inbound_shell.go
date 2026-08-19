package im

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

// Inbound shell passthrough.
//
// The TUI has long supported `$ cmd` / `! cmd` as a direct shell escape
// (update_keys.go). The IM daemon bridge had no equivalent: a `$ ls` message
// fell through to InboundRouteMessage and was sent to the LLM as a prompt —
// the model might run it via run_command, but the raw output never reached
// the IM client through the deterministic emit path (only the model's
// summarized reply did).
//
// This file routes `$ cmd` / `! cmd` inbound messages to a direct shell
// execution and pushes the captured output back over the emitter, mirroring
// the TUI behavior. Security posture is identical to the TUI escape: the IM
// binding is an authenticated control channel (same trust level as the
// terminal user), and slash commands like /restart already execute
// privileged actions from IM.

const (
	// inboundShellTimeout bounds a passthrough shell command. Generous
	// enough for builds, short enough that a forgotten IM command cannot
	// wedge the bridge.
	inboundShellTimeout = 10 * time.Minute
	// inboundShellMaxOutput caps what we push back to the IM adapter —
	// messaging platforms reject long messages (Telegram ~4096 chars).
	inboundShellMaxOutput = 3500
)

// IsInboundShellCommand reports whether the trimmed inbound text is a shell
// passthrough (`$ cmd` or `! cmd` with a non-empty command).
func IsInboundShellCommand(text string) bool {
	cmd, ok := splitInboundShellCommand(text)
	return ok && cmd != ""
}

// splitInboundShellCommand extracts the command after the `$`/`!` prefix.
func splitInboundShellCommand(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	if trimmed[0] != '$' && trimmed[0] != '!' {
		return "", false
	}
	return strings.TrimSpace(trimmed[1:]), true
}

// emitShellText pushes text back to the IM client, honoring the test hook.
func (b *DaemonBridge) emitShellText(s string) error {
	if b.emitTextOverride != nil {
		return b.emitTextOverride(s)
	}
	return b.emitter.EmitText(s)
}

// RunInboundShellAsync executes a `$ cmd` / `! cmd` passthrough and pushes
// the output back via emit. Package-level so both inbound paths share one
// implementation: the daemon bridge (DaemonBridge.handleShellInbound) and
// the TUI remote-inbound handler (im-bound sessions with an attached TUI).
// Runs asynchronously; emit must be safe for concurrent use.
func RunInboundShellAsync(text string, emit func(string) error) {
	cmdText, ok := splitInboundShellCommand(text)
	if !ok || cmdText == "" {
		_ = emit("Usage: $ <command> (or ! <command>)")
		return
	}
	_ = emit(fmt.Sprintf("⌨ Running: %s", cmdText))
	safego.Go("im.inboundShell", func() {
		ctx, cancel := context.WithTimeout(context.Background(), inboundShellTimeout)
		defer cancel()

		start := time.Now()
		// sh -c, not bash: matches the TUI escape semantics and keeps the
		// dependency footprint identical across platforms.
		cmd := exec.CommandContext(ctx, "sh", "-c", cmdText)
		out, err := cmd.CombinedOutput()
		elapsed := time.Since(start)

		var sb strings.Builder
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			sb.WriteString(fmt.Sprintf("⏱ Timed out after %s\n", inboundShellTimeout))
		}
		body := strings.TrimRight(string(out), "\n")
		if body == "" && err != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			sb.WriteString(fmt.Sprintf("(no output, exit error: %v)", err))
		} else if body != "" {
			if len(body) > inboundShellMaxOutput {
				// Snap to a rune boundary: byte-slicing mid-rune produces invalid
				// UTF-8, which Telegram's API rejects with a 400.
				cut := util.SnapToRuneStart(body, inboundShellMaxOutput)
				body = body[:cut] + fmt.Sprintf("\n… (truncated, %d more chars)", len(body)-cut)
			}
			sb.WriteString(body)
		}
		sb.WriteString(fmt.Sprintf("\n— exit %v in %s", exitCodeOrErr(err), elapsed.Round(time.Millisecond)))
		debug.Log("daemon-bridge", "inbound shell '%s' done in %s (err=%v)", cmdText, elapsed.Round(time.Millisecond), err)
		if err := emit(sb.String()); err != nil {
			debug.Log("daemon-bridge", "inbound shell '%s' emit failed: %v", cmdText, err)
		}
	})
}

// handleShellInbound executes the shell command and pushes the output back
// to the IM client via the emitter. Runs the command asynchronously so the
// inbound loop is not blocked; the emitter is safe for concurrent use.
func (b *DaemonBridge) handleShellInbound(text string) {
	RunInboundShellAsync(text, b.emitShellText)
}

// exitCodeOrErr renders an exec error's exit code, or a generic marker.
func exitCodeOrErr(err error) string {
	if err == nil {
		return "0"
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Sprintf("%d", ee.ExitCode())
	}
	return err.Error()
}
