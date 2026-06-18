//go:build !darwin && !linux

package tool

import (
	"fmt"
	"runtime"
	"strings"
)

// ── Stub for unsupported platforms (windows, etc.) ──────────────────────────

func ghosttyBinaryPath() string { return "" }

func (g *GhosttyTool) executeStatus() Result {
	if !ghosttyAvailable() {
		return Result{Content: "ghostty: not detected (TERM_PROGRAM != ghostty)"}
	}
	return Result{Content: fmt.Sprintf("ghostty: detected but platform %s/%s is not supported (only darwin and linux)", runtime.GOOS, runtime.GOARCH)}
}

func (g *GhosttyTool) executeList() Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeSplit(terminalID, direction string, size int, command, workingDir string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeNewTab(command, workingDir string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeNewWindow(command, workingDir string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeFocus(terminalID string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeClose(terminalID string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeInput(terminalID, text string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeSendKey(terminalID, key, modifiers string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeAction(terminalID, actionStr string) Result {
	return unsupportedResult()
}

func (g *GhosttyTool) executeSelectTab(tabIndex int) Result {
	return unsupportedResult()
}

func unsupportedResult() Result {
	return Result{IsError: true, Content: fmt.Sprintf("ghostty tool is not supported on %s/%s", runtime.GOOS, runtime.GOARCH)}
}

// Ensure strings is referenced (used in some conditional branches).
var _ = strings.TrimSpace
