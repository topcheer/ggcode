//go:build darwin

package a2a

import (
	"os/exec"
	"syscall"
)

// setProcessGroup is a no-op on macOS.
// macOS doesn't support Pdeathsig. Instead, avahi-publish isn't used on
// macOS (dns-sd is used via hashicorp/mdns library, which runs in-process).
// So there's no child process to orphan.
func setProcessGroup(cmd *exec.Cmd) {
	_ = cmd
}

func killProcessGroup(pid int) {
	syscall.Kill(pid, syscall.SIGTERM)
}
