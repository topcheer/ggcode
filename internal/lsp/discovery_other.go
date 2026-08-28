//go:build !windows

package lsp

import "os/exec"

// detachConsole is a no-op on non-Windows platforms: Unix children never
// inherit a console-title side channel, so probe commands need no isolation.
func detachConsole(*exec.Cmd) {}
