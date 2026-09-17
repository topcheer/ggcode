//go:build !darwin && !linux

package tool

import (
	"os/exec"
)

// sandboxWrap is a no-op on platforms without a wired sandbox backend.
// wrapShellCommandOS translates the returned error into a fail-closed tool
// error when sandbox.enabled is set, so the policy can never silently
// degrade into unsandboxed execution.
func sandboxWrap(cmd *exec.Cmd, workspace string, p *SandboxPolicy) (bool, error) {
	return false, errSandboxUnavailable
}
