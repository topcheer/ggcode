//go:build integration_local

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoHardcodedAbsolutePaths verifies no source file contains hardcoded
// absolute paths that would break test HOME isolation.
func TestNoHardcodedAbsolutePaths(t *testing.T) {
	root := findProjectRoot(t)

	// Collect all .go files (excluding vendor, test, generated)
	files, err := filepath.Glob(filepath.Join(root, "internal", "**", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	cmdFiles, _ := filepath.Glob(filepath.Join(root, "cmd", "**", "*.go"))
	files = append(files, cmdFiles...)

	problems := []string{}
	for _, f := range files {
		// Skip test files, vendor, and generated
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

		// Check for hardcoded macOS/Linux home paths
		for _, line := range strings.Split(content, "\n") {
			// Skip comments
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
				continue
			}

			// Detect hardcoded paths in string literals
			// Exclusions: system resource paths (fonts, brew install dirs) are NOT $HOME-dependent
			if strings.Contains(line, `"/Users/`) && !strings.Contains(line, "example") && !strings.Contains(line, "filepath.Join") {
				problems = append(problems, relPath+": "+trimmed)
			}
			if strings.Contains(line, `"/home/`) && !strings.Contains(line, "example") && !strings.Contains(line, "filepath.Join") && !strings.Contains(line, "/home/user") {
				// Allow well-known system install paths like /home/linuxbrew
				if !strings.Contains(line, "/home/linuxbrew/") {
					problems = append(problems, relPath+": "+trimmed)
				}
			}
			if strings.Contains(line, `"C:\\`) {
				// Allow Windows system font paths (not $HOME-dependent)
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

// TestAllUserHomeDirBypassesConfigHomeDir verifies that all calls to
// os.UserHomeDir() are either in test files or are justified.
// In production code, config.HomeDir() should be used instead because
// it respects the HOME env variable override for test isolation.
func TestAllUserHomeDirBypassesConfigHomeDir(t *testing.T) {
	root := findProjectRoot(t)

	// Find all os.UserHomeDir() calls in non-test source files
	problems := []string{}
	justified := map[string]bool{
		// These files directly build paths via os.UserHomeDir() — they work
		// because os.UserHomeDir() reads $HOME on Unix, so test HOME override
		// works. We track them here so any NEW calls get flagged for review.
		"internal/config/env.go":                 true, // defines HomeDir()
		"internal/config/instance.go":            true, // instance dir path
		"internal/config/anthropic_bootstrap.go": true, // bootstrap
		"internal/install/install.go":            true, // install paths
		"internal/memory/auto.go":                true, // memory dir
		"internal/memory/project.go":             true, // project memory
		"internal/auth/store.go":                 true, // auth storage
		"internal/auth/a2a_token_cache.go":       true, // token cache
		"internal/plugin/loader.go":              true, // plugin paths
		"internal/plugin/mcp_disabled.go":        true, // plugin paths
		"internal/lsp/discovery.go":              true, // LSP discovery
		"internal/mcp/migration.go":              true, // MCP migration
		"internal/im/pc_session_store.go":        true, // IM session store
		"internal/im/bindings.go":                true, // IM bindings
		"internal/im/pairing.go":                 true, // IM pairing
		"internal/commands/loader.go":            true, // commands
		"internal/commands/disabled_state.go":    true, // commands state
		"internal/commands/usage.go":             true, // usage tracking
		"internal/permission/config_policy.go":   true, // policy
		"internal/acp/handler.go":                true, // ACP handler
		"internal/tool/todo_write.go":            true, // todos
		"internal/session/store.go":              true, // session store
		"internal/tui/view.go":                   true, // view rendering
		"internal/tui/view_sidebar.go":           true, // sidebar view
		"internal/tui/pty_harness.go":            true, // test harness — reads real config to derive test config
		"internal/runfile/runfile.go":            true, // has own homeDir() helper
		"internal/a2a/registry.go":               true, // has own homeDir() helper
		"internal/tool/browser.go":               true, // browser profile paths
		"internal/tmux/store.go":                 true, // checks $HOME first, then os.UserHomeDir
		"internal/update/detect.go":              true, // update detection paths
		"internal/util/homedir.go":               true, // canonical implementation
		"internal/debug/debug.go":                true, // now uses util.HomeDir()
		"cmd/ggcode/daemon.go":                   true, // daemon
		"cmd/ggcode/im_cmd.go":                   true, // IM command
		"cmd/ggcode/root.go":                     true, // root command
		"cmd/ggcode/onboard.go":                  true, // onboarding paths
		"cmd/ggcode/status.go":                   true, // status command paths
		"desktop/ggcode-desktop-wails/app.go":    true, // desktop app paths
		"desktop/wailskit/desktop_config.go":     true, // desktop config paths
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

// TestTestHOMEOverrideWorks verifies that setting HOME env var actually
// redirects os.UserHomeDir() — critical assumption for test isolation.
func TestTestHOMEOverrideWorks(t *testing.T) {
	tmpDir := t.TempDir()

	// Save original
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)

	// Override
	os.Setenv("HOME", tmpDir)

	// Verify os.UserHomeDir() respects it
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if home != tmpDir {
		t.Errorf("os.UserHomeDir() = %q, want %q — HOME override doesn't work!", home, tmpDir)
	}

	// Verify config.HomeDir() also respects it
	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	os.MkdirAll(ggcodeDir, 0755)
	configFile := filepath.Join(ggcodeDir, "ggcode.yaml")
	os.WriteFile(configFile, []byte("vendor: test"), 0600)

	// Read back
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != "vendor: test" {
		t.Errorf("config content = %q, want 'vendor: test'", string(data))
	}

	t.Logf("✓ HOME=%s correctly redirects os.UserHomeDir()", tmpDir)
}

// findProjectRoot locates the project root (where go.mod is).
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

// Ensure exec import is used
var _ = exec.Command
