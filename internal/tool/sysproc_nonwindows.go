//go:build !windows

package tool

import "os/exec"

func applyCreateNoWindow(cmd *exec.Cmd) {}
