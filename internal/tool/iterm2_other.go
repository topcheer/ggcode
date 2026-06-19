//go:build !darwin

package tool

import (
	"fmt"
	"runtime"
)

// ── Stub for unsupported platforms (Linux, Windows, etc.) ────────────────────
// iTerm2 is macOS-only, so all actions return an error on other platforms.

func (t *Iterm2Tool) executeStatus() Result {
	if !iterm2Available() {
		return Result{Content: "iterm2: not detected (TERM_PROGRAM != iTerm.app)"}
	}
	return Result{Content: fmt.Sprintf("iterm2: detected but platform %s/%s is not supported (iTerm2 is macOS-only)", runtime.GOOS, runtime.GOARCH)}
}

func (t *Iterm2Tool) executeList() Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeSplit(sessionID, direction string, size int, command, workingDir string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeNewTab(command, workingDir string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeNewWindow(command, workingDir string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeFocus(sessionID string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeClose(sessionID string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeSelectTab(tabIndex int) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeInput(sessionID, text string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeSendKey(sessionID, key, modifiers string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeResize(sessionID, axis string, increment int) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeGetText(sessionID string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeSetTitle(sessionID, title string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeProfile(sessionID, profileName string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeBadge(sessionID, badgeText string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeBroadcast(subAction string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeMark(subAction string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeClear(sessionID string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeMenuAction(menuItem string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeReloadConfig() Result {
	return iterm2UnsupportedResult()
}

func iterm2UnsupportedResult() Result {
	return Result{IsError: true, Content: fmt.Sprintf("iterm2 tool is not supported on %s/%s (iTerm2 is macOS-only)", runtime.GOOS, runtime.GOARCH)}
}
