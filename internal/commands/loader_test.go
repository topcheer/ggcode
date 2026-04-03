package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	cmdDir := filepath.Join(tmpDir, ".ggcode", "commands")
	os.MkdirAll(cmdDir, 0755)

	os.WriteFile(filepath.Join(cmdDir, "review-pr.md"), []byte("Review the PR carefully"), 0644)
	os.WriteFile(filepath.Join(cmdDir, "test.md"), []byte("Run tests\nFocus on unit tests"), 0644)

	l := NewLoader(tmpDir)
	cmds := l.Load()

	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}

	if cmd, ok := cmds["review-pr"]; !ok {
		t.Error("missing review-pr command")
	} else if cmd.Description != "Review the PR carefully" {
		t.Errorf("wrong description: %q", cmd.Description)
	}

	if cmd, ok := cmds["test"]; !ok {
		t.Error("missing test command")
	} else if cmd.Template != "Run tests\nFocus on unit tests" {
		t.Errorf("wrong template: %q", cmd.Template)
	}
}

func TestCommand_Expand(t *testing.T) {
	c := &Command{
		Name:     "test",
		Template: "Review $FILE in $DIR",
	}
	result := c.Expand(map[string]string{"FILE": "main.go", "DIR": "/tmp"})
	if result != "Review main.go in /tmp" {
		t.Errorf("unexpected: %q", result)
	}
}
