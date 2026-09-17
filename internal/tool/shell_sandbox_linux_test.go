//go:build linux

package tool

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// sandboxWrap on Linux must rewrite cmd into the self-launcher protocol:
// [<ggcode binary>] __ggcode_sandbox_launch <payload-json> <shell> <args...>.
// The main() hook in cmd/ggcode parses exactly this argv shape.
func TestSandboxWrapLinuxLauncherShape(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}
	workspace := t.TempDir()
	origArgs := []string{shell, "-c", "echo hi"}
	cmd := &exec.Cmd{Path: shell, Args: origArgs}
	p := NewSandboxPolicy(true, nil, nil)

	wrapped, err := sandboxWrap(cmd, workspace, p)
	if err != nil {
		t.Fatalf("sandboxWrap: %v", err)
	}
	if !wrapped {
		t.Fatal("enabled policy must wrap on linux")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if cmd.Path != self {
		t.Errorf("cmd.Path = %q, want self %q", cmd.Path, self)
	}
	if len(cmd.Args) < 4 {
		t.Fatalf("cmd.Args too short: %v", cmd.Args)
	}
	if cmd.Args[0] != self {
		t.Errorf("Args[0] = %q, want self", cmd.Args[0])
	}
	if cmd.Args[1] != sandboxLaunchMarker {
		t.Errorf("Args[1] = %q, want marker %q", cmd.Args[1], sandboxLaunchMarker)
	}
	payload, err := decodeSandboxLaunchPayload(cmd.Args[2])
	if err != nil {
		t.Fatalf("decode payload arg: %v", err)
	}
	if payload.AllowNetwork != p.AllowNetwork {
		t.Errorf("payload.AllowNetwork = %v, want %v", payload.AllowNetwork, p.AllowNetwork)
	}
	found := false
	for _, path := range payload.WritePaths {
		if path == workspace {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("workspace %q missing from payload.WritePaths %v", workspace, payload.WritePaths)
	}
	if cmd.Args[3] != shell {
		t.Errorf("Args[3] = %q, want original shell %q", cmd.Args[3], shell)
	}
	gotTail := cmd.Args[4:]
	wantTail := origArgs[1:]
	if len(gotTail) != len(wantTail) {
		t.Fatalf("tail = %v, want %v", gotTail, wantTail)
	}
	for i := range wantTail {
		if gotTail[i] != wantTail[i] {
			t.Errorf("tail[%d] = %q, want %q", i, gotTail[i], wantTail[i])
		}
	}
}

// The linux init must wire the launcher entry consumed by the cmd/ggcode
// main hook; a nil entry there is treated as an internal error (exit 126).
func TestSandboxLaunchEntryWiredOnLinux(t *testing.T) {
	if SandboxLaunchEntry == nil {
		t.Fatal("SandboxLaunchEntry must be wired by the linux init")
	}
}

// The generated cBPF program must be well-formed: non-empty, terminated by a
// RET-class instruction, and available for every arch ggcode ships.
func TestSeccompNetworkDenyFilterWellFormed(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		prog, err := sbSeccompNetworkDenyFilter(arch)
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		if len(prog) == 0 {
			t.Fatalf("%s: empty filter program", arch)
		}
		last := prog[len(prog)-1]
		if last.Code != sbBpfRetK {
			t.Errorf("%s: last instruction must be RET, got code %d", arch, last.Code)
		}
		if last.K == sbSeccompRetAllow {
			t.Errorf("%s: fallthrough must not be ALLOW", arch)
		}
	}
	if _, err := sbSeccompNetworkDenyFilter(runtime.GOARCH); err != nil {
		t.Errorf("runtime arch %s: %v", runtime.GOARCH, err)
	}
}
