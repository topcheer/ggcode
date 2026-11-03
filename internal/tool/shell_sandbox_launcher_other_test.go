//go:build !linux

package tool

import "testing"

func TestSandboxLaunchEntryNilOffLinux(t *testing.T) {
	if SandboxLaunchEntry != nil {
		t.Fatal("SandboxLaunchEntry must stay nil on non-linux builds")
	}
}
