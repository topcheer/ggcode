//go:build !darwin

package tool

import (
	"context"
	"fmt"
	"runtime"
)

// ── Stub for unsupported platforms (Linux, Windows, etc.) ────────────────────
// iTerm2 is macOS-only, so all actions return an error on other platforms.

func (t *Iterm2Tool) executeStatus(ctx context.Context) Result {
	if !iterm2Available() {
		return Result{Content: "iterm2: not detected (TERM_PROGRAM != iTerm.app)"}
	}
	return Result{Content: fmt.Sprintf("iterm2: detected but platform %s/%s is not supported (iTerm2 is macOS-only)", runtime.GOOS, runtime.GOARCH)}
}

func (t *Iterm2Tool) executeList(ctx context.Context) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeSplit(ctx context.Context, sessionID, direction string, size int, command, workingDir string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeNewTab(ctx context.Context, command, workingDir string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeNewWindow(ctx context.Context, command, workingDir string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeFocus(ctx context.Context, sessionID string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeClose(ctx context.Context, sessionID string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeSelectTab(ctx context.Context, tabIndex int) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeInput(ctx context.Context, sessionID, text string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeSendKey(ctx context.Context, sessionID, key, modifiers string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeResize(ctx context.Context, sessionID, axis string, increment int) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeGetText(ctx context.Context, sessionID string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeSetTitle(ctx context.Context, sessionID, title string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeProfile(ctx context.Context, sessionID, profileName string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeBadge(ctx context.Context, sessionID, badgeText string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeBroadcast(ctx context.Context, subAction string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeMark(ctx context.Context, subAction string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeClear(ctx context.Context, sessionID string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeMenuAction(ctx context.Context, menuItem string) Result {
	return iterm2UnsupportedResult()
}

func (t *Iterm2Tool) executeReloadConfig(ctx context.Context) Result {
	return iterm2UnsupportedResult()
}

func iterm2UnsupportedResult() Result {
	return Result{IsError: true, Content: fmt.Sprintf("iterm2 tool is not supported on %s/%s (iTerm2 is macOS-only)", runtime.GOOS, runtime.GOARCH)}
}
