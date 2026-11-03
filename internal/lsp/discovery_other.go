//go:build !windows

package lsp

import "os/exec"

func detachConsole(*exec.Cmd) {}
