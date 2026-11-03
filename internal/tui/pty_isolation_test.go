//go:build integration_local

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPTY_TestHomeIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	realHome, _ := os.UserHomeDir()
	realConfig := filepath.Join(realHome, ".ggcode", "ggcode.yaml")
	realKeys := filepath.Join(realHome, ".ggcode", "keys.env")
	realConfigBefore, _ := os.ReadFile(realConfig)
	realKeysBefore, _ := os.ReadFile(realKeys)

	h := startGGCode(t, ptyOptions{})
	h.waitForText("Type a message", 5*time.Second)

	testGGCode := filepath.Join(h.tmpDir, ".ggcode")
	ggcodeYaml := filepath.Join(testGGCode, "ggcode.yaml")
	if _, err := os.Stat(ggcodeYaml); err != nil {
		t.Errorf("test ggcode.yaml not created in tmpdir: %v", err)
	}
	t.Logf("test config: %s", ggcodeYaml)

	testData, _ := os.ReadFile(ggcodeYaml)
	if strings.Contains(string(testData), "${") {

		t.Log("test config uses env var references")
	}
	if strings.Contains(string(testData), "sk-") && !strings.Contains(string(testData), "sk-pty-test") {
		t.Error("test config may contain real API key!")
	}

	h.quit()

	realConfigAfter, _ := os.ReadFile(realConfig)
	realKeysAfter, _ := os.ReadFile(realKeys)

	if string(realConfigBefore) != string(realConfigAfter) {
		t.Error("REAL ggcode.yaml was modified during test!")
	}
	if string(realKeysBefore) != string(realKeysAfter) {
		t.Error("REAL keys.env was modified during test!")
	}

	t.Log("✓ Real config untouched, test config fully isolated")
}

func TestPTY_TestWorkspaceIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	h.waitForText("Type a message", 5*time.Second)

	wsDir := filepath.Join(h.tmpDir, "workspace")
	if _, err := os.Stat(wsDir); err != nil {
		t.Errorf("workspace dir not created: %v", err)
	}

	wsConfig := filepath.Join(wsDir, ".ggcode", "ggcode.yaml")
	if _, err := os.Stat(wsConfig); err != nil {
		t.Errorf("workspace .ggcode/ggcode.yaml not created: %v", err)
	}

	h.quit()
	t.Log("✓ Workspace fully isolated in tmpdir")
}

func TestPTY_TestConfigNoRealSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	h.waitForText("Type a message", 5*time.Second)

	testConfig := filepath.Join(h.tmpDir, ".ggcode", "ggcode.yaml")
	data, err := os.ReadFile(testConfig)
	if err != nil {
		t.Fatalf("read test config: %v", err)
	}

	content := string(data)

	dangerousPatterns := []string{
		"sk-proj-",
		"sk-ant-",
		"sk-",
	}
	for _, p := range dangerousPatterns {
		if p == "sk-" {

			for _, line := range strings.Split(content, "\n") {
				if strings.Contains(line, "sk-") && !strings.Contains(line, "sk-pty-test") && !strings.Contains(line, "127.0.0.1") {
					t.Errorf("potential real key in test config: %s", line)
				}
			}
		}
	}

	h.quit()
	t.Log("✓ No real secrets in test config")
}
