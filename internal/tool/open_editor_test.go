package tool

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenEditor_NonExistentFile(t *testing.T) {
	tool := OpenEditorTool{}
	input, _ := json.Marshal(map[string]string{
		"path":        "/nonexistent/path/to/file.go",
		"description": "test",
	})
	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for non-existent file")
	}
	if !strings.Contains(result.Content, "file not found") {
		t.Errorf("expected 'file not found' message, got: %s", result.Content)
	}
}

func TestOpenEditor_MissingPath(t *testing.T) {
	tool := OpenEditorTool{}
	input, _ := json.Marshal(map[string]string{
		"description": "test",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Error("expected error for missing path")
	}
}

func TestOpenEditor_DetectEditorFromEnv(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("GGCODE_EDITOR", "myeditor")

	got := detectEditor()
	if got != "myeditor" {
		t.Errorf("expected 'myeditor', got %q", got)
	}
}

func TestOpenEditor_DetectEditorPriority(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	t.Setenv("VISUAL", "emacs")
	t.Setenv("GGCODE_EDITOR", "")

	// VISUAL should win over EDITOR
	got := detectEditor()
	if got != "emacs" {
		t.Errorf("expected 'emacs' (VISUAL), got %q", got)
	}
}

func TestOpenEditor_EditorName(t *testing.T) {
	cases := map[string]string{
		"code":     "VS Code",
		"cursor":   "Cursor",
		"nvim":     "Neovim",
		"vim":      "Vim",
		"subl":     "Sublime Text",
		"open":     "system default",
		"myeditor": "myeditor",
	}
	for binary, expected := range cases {
		if got := editorName(binary); got != expected {
			t.Errorf("editorName(%q) = %q, want %q", binary, got, expected)
		}
	}
}

func TestOpenEditor_BuildCommandVSCode(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.go")
	os.WriteFile(tmpFile, []byte("package main\n"), 0644)

	// VS Code with line number
	cmd := buildEditorCommand("code", tmpFile, 42, 0)
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}
	// Should use --goto with file:line
	found := false
	for _, arg := range cmd.Args {
		if strings.Contains(arg, ":42") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected :42 in args, got %v", cmd.Args)
	}

	// VS Code with line and column
	cmd = buildEditorCommand("code", tmpFile, 42, 10)
	found = false
	for _, arg := range cmd.Args {
		if strings.Contains(arg, ":42:10") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected :42:10 in args, got %v", cmd.Args)
	}
}

func TestOpenEditor_BuildCommandVim(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.go")
	os.WriteFile(tmpFile, []byte("package main\n"), 0644)

	// Vim with line number
	cmd := buildEditorCommand("vim", tmpFile, 10, 0)
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}
	hasLineFlag := false
	for _, arg := range cmd.Args {
		if arg == "+10" {
			hasLineFlag = true
		}
	}
	if !hasLineFlag {
		t.Errorf("expected +10 flag for vim, got %v", cmd.Args)
	}
}

func TestOpenEditor_BuildCommandJetBrains(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.go")
	os.WriteFile(tmpFile, []byte("package main\n"), 0644)

	cmd := buildEditorCommand("idea", tmpFile, 25, 0)
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}
	hasLine := false
	for _, arg := range cmd.Args {
		if arg == "25" {
			hasLine = true
		}
	}
	if !hasLine {
		t.Errorf("expected --line 25 for IntelliJ, got %v", cmd.Args)
	}
}

func TestOpenEditor_BuildCommandFallback(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(tmpFile, []byte("hello\n"), 0644)

	// Unknown editor should still produce a command
	cmd := buildEditorCommand("myeditor", tmpFile, 0, 0)
	if cmd == nil {
		t.Fatal("expected non-nil command for unknown editor")
	}
}

func TestOpenEditor_NoEditorDetected(t *testing.T) {
	// Clear all env vars
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("GGCODE_EDITOR", "")
	t.Setenv("GID_EDITOR", "")

	// detectEditor will try LookPath for IDEs, which might find something.
	// We just verify it doesn't crash.
	_ = detectEditor()
}

func TestOpenEditor_IdeLaunchers(t *testing.T) {
	launchers := ideLaunchers()
	if len(launchers) == 0 {
		t.Error("expected non-empty IDE launcher list")
	}
	// Verify code is in the list
	found := false
	for _, l := range launchers {
		if l == "code" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'code' in IDE launcher list")
	}
}

func TestOpenEditor_LaunchAndVerify(t *testing.T) {
	// Use a real script as the "editor" to verify the launch mechanism works.
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake-editor.sh")
	markerPath := filepath.Join(tmpDir, "marker")
	script := "#!/bin/sh\necho launched > " + markerPath + "\n"
	os.WriteFile(scriptPath, []byte(script), 0755)

	targetFile := filepath.Join(tmpDir, "target.txt")
	os.WriteFile(targetFile, []byte("content\n"), 0644)

	cmd := exec.Command(scriptPath, targetFile)
	if err := startDetached(cmd); err != nil {
		t.Fatalf("startDetached failed: %v", err)
	}

	// Wait for the detached process to complete (it exits immediately after
	// writing the marker). This is more reliable than polling under CI load
	// where the detached process may be starved of CPU.
	// Ignore the error — the process may have already exited (ESRCH) when
	// running in a new session.
	_ = cmd.Wait()

	// Fall back to polling if Wait() didn't catch the process (can happen
	// with Setsid on some platforms).
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(markerPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("marker file not created: %v", err)
	}
	if strings.TrimSpace(string(data)) != "launched" {
		t.Errorf("expected 'launched', got %q", string(data))
	}
}
