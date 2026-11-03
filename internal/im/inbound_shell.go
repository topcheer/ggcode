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

const (
	inboundShellTimeout   = 10 * time.Minute
	inboundShellMaxOutput = 3500
)

func IsInboundShellCommand(text string) bool {
	cmd, ok := splitInboundShellCommand(text)
	return ok && cmd != ""
}

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

func (b *DaemonBridge) emitShellText(s string) error {
	if b.emitTextOverride != nil {
		return b.emitTextOverride(s)
	}
	return b.emitter.EmitText(s)
}

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

func (b *DaemonBridge) handleShellInbound(text string) {
	RunInboundShellAsync(text, b.emitShellText)
}

func exitCodeOrErr(err error) string {
	if err == nil {
		return "0"
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Sprintf("%d", ee.ExitCode())
	}
	return err.Error()
}
