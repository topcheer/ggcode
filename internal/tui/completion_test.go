package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/topcheer/ggcode/internal/commands"
)

func TestParseMentions(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0644)
	os.MkdirAll(filepath.Join(dir, "internal"), 0755)
	os.WriteFile(filepath.Join(dir, "internal", "util.go"), []byte("package internal"), 0644)

	tests := []struct {
		name   string
		input  string
		wantN  int
		wantOk bool
	}{
		{"single file", "fix this @main.go", 1, true},
		{"multiple files", "@main.go and @README.md", 2, true},
		{"directory", "look at @internal/", 1, true},
		{"no mentions", "plain text", 0, true},
		{"max 5", "@main.go @README.md @internal/ @main.go @README.md @internal/util.go", 5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mentions, err := ParseMentions(tt.input, dir)
			if (err == nil) != tt.wantOk {
				t.Fatalf("ParseMentions error = %v, wantOk %v", err, tt.wantOk)
			}
			if len(mentions) != tt.wantN {
				t.Errorf("got %d mentions, want %d", len(mentions), tt.wantN)
			}
		})
	}
}

func TestExpandMentions(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc main() {}"), 0644)
	os.MkdirAll(filepath.Join(dir, "cmd"), 0755)
	os.WriteFile(filepath.Join(dir, "cmd", "root.go"), []byte("package cmd"), 0644)

	result, err := ExpandMentions("review @hello.go and @cmd/", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result, "package main") {
		t.Error("expanded message should contain file content")
	}
	if !contains(result, "root.go") {
		t.Error("expanded message should list directory contents")
	}
	// @mentions are stripped from the message body but appear as headers in the references section
	if contains(result, "review  and") {
		t.Error("cleaned message should remove @mention tokens")
	}
}

func TestCompleteMention(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "makefile"), []byte(""), 0644)
	os.MkdirAll(filepath.Join(dir, "internal"), 0755)

	completions := CompleteMention("m", dir)
	if len(completions) != 2 {
		t.Errorf("expected 2 completions for 'm', got %d: %v", len(completions), completions)
	}

	completions = CompleteMention("internal/", dir)
	// Directory is empty so 0 completions is valid
}

func TestParseMentions_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "safe.txt"), []byte("safe content"), 0644)

	tests := []struct {
		name  string
		input string
		wantN int
	}{
		{"dotdot", "@../../etc/passwd", 0},
		{"absolute path", "@/etc/passwd", 0},
		{"dotdot variant", "@../secret", 0},
		{"nested dotdot", "@internal/../../etc/passwd", 0},
		{"safe file", "@safe.txt", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mentions, err := ParseMentions(tt.input, dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(mentions) != tt.wantN {
				t.Errorf("got %d mentions, want %d", len(mentions), tt.wantN)
			}
		})
	}
}

func TestDetectMention(t *testing.T) {
	ti := textinput.New()
	ti.SetValue("fix @internal/")
	ti.SetCursor(len(ti.Value()))

	active, prefix := DetectMention(ti)
	if !active {
		t.Error("expected mention to be detected")
	}
	if prefix != "internal/" {
		t.Errorf("expected prefix 'internal/', got %q", prefix)
	}

	ti.SetValue("plain text")
	ti.SetCursor(len(ti.Value()))
	active, _ = DetectMention(ti)
	if active {
		t.Error("expected no mention detection")
	}
}

func TestCompleteSlashCommandOnlyIncludesLegacyCommands(t *testing.T) {
	matches := CompleteSlashCommand("/de", map[string]*commands.Command{
		"deploy": {
			Name:          "deploy",
			UserInvocable: true,
			LoadedFrom:    commands.LoadedFromCommands,
		},
		"debug": {
			Name:          "debug",
			UserInvocable: true,
			LoadedFrom:    commands.LoadedFromSkills,
		},
	})

	if len(matches) != 1 || matches[0] != "/deploy" {
		t.Fatalf("matches = %v, want [/deploy]", matches)
	}
}

func TestCompleteSlashCommandIncludesHarness(t *testing.T) {
	matches := CompleteSlashCommand("/har", nil)
	if len(matches) != 1 || matches[0] != "/harness" {
		t.Fatalf("matches = %v, want [/harness]", matches)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
