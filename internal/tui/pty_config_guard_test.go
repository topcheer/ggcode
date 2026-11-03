//go:build integration_local

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoHardcodedAbsolutePaths(t *testing.T) {
	root := findProjectRoot(t)

	files, err := filepath.Glob(filepath.Join(root, "internal", "**", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	cmdFiles, _ := filepath.Glob(filepath.Join(root, "cmd", "**", "*.go"))
	files = append(files, cmdFiles...)

	problems := []string{}
	for _, f := range files {

		base := filepath.Base(f)
		if strings.HasSuffix(base, "_test.go") {
			continue
		}
		if strings.Contains(f, "vendor") {
			continue
		}

		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		content := string(data)
		relPath, _ := filepath.Rel(root, f)

		for _, line := range strings.Split(content, "\n") {

			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
				continue
			}

			if strings.Contains(line, `"/Users/`) && !strings.Contains(line, "example") && !strings.Contains(line, "filepath.Join") {
				problems = append(problems, relPath+": "+trimmed)
			}
			if strings.Contains(line, `"/home/`) && !strings.Contains(line, "example") && !strings.Contains(line, "filepath.Join") && !strings.Contains(line, "/home/user") {

				if !strings.Contains(line, "/home/linuxbrew/") {
					problems = append(problems, relPath+": "+trimmed)
				}
			}
			if strings.Contains(line, `"C:\\`) {

				if !strings.Contains(line, "Fonts") {
					problems = append(problems, relPath+": "+trimmed)
				}
			}
		}
	}

	if len(problems) > 0 {
		t.Errorf("found %d lines with hardcoded absolute paths:\n%s",
			len(problems), strings.Join(problems, "\n"))
	}
}

func TestAllUserHomeDirBypassesConfigHomeDir(t *testing.T) {
	root := findProjectRoot(t)

	problems := []string{}
	justified := map[string]bool{

		"internal/config/env.go":                 true,
		"internal/config/instance.go":            true,
		"internal/config/anthropic_bootstrap.go": true,
		"internal/install/install.go":            true,
		"internal/memory/auto.go":                true,
		"internal/memory/project.go":             true,
		"internal/auth/store.go":                 true,
		"internal/auth/a2a_token_cache.go":       true,
		"internal/plugin/loader.go":              true,
		"internal/plugin/mcp_disabled.go":        true,
		"internal/lsp/discovery.go":              true,
		"internal/mcp/migration.go":              true,
		"internal/im/pc_session_store.go":        true,
		"internal/im/bindings.go":                true,
		"internal/im/pairing.go":                 true,
		"internal/commands/loader.go":            true,
		"internal/commands/disabled_state.go":    true,
		"internal/commands/usage.go":             true,
		"internal/permission/config_policy.go":   true,
		"internal/acp/handler.go":                true,
		"internal/tool/todo_write.go":            true,
		"internal/session/store.go":              true,
		"internal/tui/view.go":                   true,
		"internal/tui/view_sidebar.go":           true,
		"internal/tui/pty_harness.go":            true,
		"internal/runfile/runfile.go":            true,
		"internal/a2a/registry.go":               true,
		"internal/tool/browser.go":               true,
		"internal/tmux/store.go":                 true,
		"internal/update/detect.go":              true,
		"internal/util/homedir.go":               true,
		"internal/debug/debug.go":                true,
		"cmd/ggcode/daemon.go":                   true,
		"cmd/ggcode/im_cmd.go":                   true,
		"cmd/ggcode/root.go":                     true,
		"cmd/ggcode/onboard.go":                  true,
		"cmd/ggcode/status.go":                   true,
		"desktop/ggcode-desktop-wails/app.go":    true,
		"desktop/wailskit/desktop_config.go":     true,
	}

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, "vendor") || strings.Contains(path, ".git") {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		if strings.Contains(string(data), "os.UserHomeDir()") {
			if !justified[relPath] {
				problems = append(problems, relPath)
			}
		}
		return nil
	})

	if len(problems) > 0 {
		t.Errorf("new os.UserHomeDir() calls found in (add to justified list or switch to config.HomeDir()):\n  %s",
			strings.Join(problems, "\n  "))
	}
}

func TestTestHOMEOverrideWorks(t *testing.T) {
	tmpDir := t.TempDir()

	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)

	os.Setenv("HOME", tmpDir)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if home != tmpDir {
		t.Errorf("os.UserHomeDir() = %q, want %q — HOME override doesn't work!", home, tmpDir)
	}

	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	os.MkdirAll(ggcodeDir, 0755)
	configFile := filepath.Join(ggcodeDir, "ggcode.yaml")
	os.WriteFile(configFile, []byte("vendor: test"), 0600)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != "vendor: test" {
		t.Errorf("config content = %q, want 'vendor: test'", string(data))
	}

	t.Logf("✓ HOME=%s correctly redirects os.UserHomeDir()", tmpDir)
}

func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

var _ = exec.Command
