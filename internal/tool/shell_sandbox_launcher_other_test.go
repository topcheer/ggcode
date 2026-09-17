//go:build !linux

package tool

import "testing"

// On non-linux builds the launcher entry stays nil and the cmd/ggcode main
// hook must treat that as an internal error (exit 126) rather than running
// the target command unsandboxed. Pin the nil contract here.
func TestSandboxLaunchEntryNilOffLinux(t *testing.T) {
	if SandboxLaunchEntry != nil {
		t.Fatal("SandboxLaunchEntry must stay nil on non-linux builds")
	}
}
