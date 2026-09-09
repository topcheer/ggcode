//go:build !darwin && !linux

package tool

import (
	"context"
	"fmt"
	"runtime"
	"strings"
)

// ── Stub for unsupported platforms (windows, etc.) ──────────────────────────

func ghosttyBinaryPath() string { return "" }

func (g *GhosttyTool) executeStatus(ctx context.Context) Result {
	if !ghosttyAvailable() {
		return Result{Content: "ghostty: not detected (TERM_PROGRAM != ghostty)"}
	}
	return Result{Content: fmt.Sprintf("ghostty: detected but platform %s/%s is not supported (only darwin and linux)", runtime.GOOS, runtime.GOARCH)}
}

func (g *GhosttyTool) executeList(ctx context.Context) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeSplit(ctx context.Context, terminalID, direction string, size int, command, workingDir string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeNewTab(ctx context.Context, command, workingDir string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeNewWindow(ctx context.Context, command, workingDir string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeFocus(ctx context.Context, terminalID string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeClose(ctx context.Context, terminalID string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeInput(ctx context.Context, terminalID, text string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeSendKey(ctx context.Context, terminalID, key, modifiers string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeAction(ctx context.Context, terminalID, actionStr string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeSelectTab(ctx context.Context, tabIndex int) Result {
	return unsupportedResult()
}

func unsupportedResult() Result {
	return Result{IsError: true, Content: fmt.Sprintf("ghostty tool is not supported on %s/%s", runtime.GOOS, runtime.GOARCH)}
}

// Ensure strings is referenced (used in some conditional branches).
var _ = strings.TrimSpace
