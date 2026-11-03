//go:build !windows

package tool

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

	marker := filepath.Join(dir, "ggcode-issue568-fake-code")
	if err := os.WriteFile(marker, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(dir, "code")
	scriptBody := "#!/bin/sh\nexec " + marker + "\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = exec.Command("pkill", "-f", marker).Run() })

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	input, _ := json.Marshal(map[string]string{
		"command":     "code --version",
		"description": "test",
	})

	tool := RunCommand{}
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

	time.Sleep(400 * time.Millisecond)

	if out, err := exec.Command("pgrep", "-f", marker).CombinedOutput(); err != nil {
		t.Fatalf("GUI process died within 400ms of Execute returning — kill-window regression (issue #568): %v %s", err, out)
	}
}
