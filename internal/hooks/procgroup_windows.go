//go:build windows

package hooks

import "os/exec"

func setProcessGroupKill(c *exec.Cmd) {}
