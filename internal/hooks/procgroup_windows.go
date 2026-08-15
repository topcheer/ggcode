//go:build windows

package hooks

import "os/exec"

// setProcessGroupKill is a no-op on Windows: Job Objects would be needed to
// kill a command's descendants, and hook commands there run through
// PowerShell which propagates cancellation to the job it creates (#413).
func setProcessGroupKill(c *exec.Cmd) {}
