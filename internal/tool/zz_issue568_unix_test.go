//go:build !windows

package tool

// Bug A (#568): GUI commands were killed ~100ms after the tool call returned
// because the GUI branch still used the request-derived context whose cancel
// was deferred at Execute scope. Verify the launched process survives the
// Execute return by a comfortable margin.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGUIProcessSurvivesExecuteReturn(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive test")
	}
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep not available")
	}
	dir := t.TempDir()

	// Unique marker so pgrep/pkill only ever match our own processes, never
	// an unrelated real "sleep 30" or editor. While the marker script runs,
	// its interpreter has cmdline "/bin/sh <marker>" — a stable liveness handle.
	marker := filepath.Join(dir, "ggcode-issue568-fake-code")
	if err := os.WriteFile(marker, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake `code` executable: exec through to the sleeping marker.
	script := filepath.Join(dir, "code")
	scriptBody := "#!/bin/sh\nexec " + marker + "\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	// Cleanup safety net regardless of test outcome.
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", marker).Run() })

	// PATH-inject the fake `code` so the plain command name resolves to it
	// (isGUICommand matches the bare first word "code", not an absolute path).
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	input, _ := json.Marshal(map[string]string{
		"command":     "code --version",
		"description": "test",
	})

	tool := RunCommand{} // zero value — no JobManager, exactly like builtin.go
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Skipf("fake GUI command could not start (sandbox/gate): %s", res.Content)
	}
	if !strings.Contains(res.Content, "GUI application launched") {
		t.Fatalf("expected GUI launch message, got: %s", res.Content)
	}

	// Wait for the exec chain (sh -c → code → exec marker) to actually reach
	// the marker process before starting the kill-window clock. Execute
	// returns right after cmd.Start() of the direct child, so under system
	// load the fork/exec chain can take longer than expected and race the
	// pgrep below without this synchronization (observed ~1/6 flake rate).
	// If the marker never appears at all, the GUI launch chain is broken.
	appearDeadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := exec.Command("pgrep", "-f", marker).CombinedOutput(); err == nil {
			break
		}
		if time.Now().After(appearDeadline) {
			t.Fatal("marker process never appeared — GUI launch chain failed (issue #568)")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Give the old kill window (SIGTERM→100ms→SIGKILL after Execute returned)
	// plenty of time to elapse, then verify the sleeper is still alive.
	time.Sleep(400 * time.Millisecond)

	if out, err := exec.Command("pgrep", "-f", marker).CombinedOutput(); err != nil {
		t.Fatalf("GUI process died within 400ms of Execute returning — kill-window regression (issue #568): %v %s", err, out)
	}
}
