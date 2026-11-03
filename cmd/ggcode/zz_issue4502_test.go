//go:build goolm

package main

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4502_MainPromptDetectsGitStatusLazily(t *testing.T) {
	b, err := os.ReadFile("root.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	idx := strings.Index(s, "buildCurrentSystemPrompt := func() (string, []string) {")
	if idx < 0 {
		t.Fatal("buildCurrentSystemPrompt not found")
	}
	end := strings.Index(s[idx+1:], "\n\tskillTool.")
	if end < 0 {
		end = len(s) - idx - 1
	}
	body := s[idx : idx+1+end]
	if !strings.Contains(body, "detectGitStatus(workingDir)") {
		t.Fatal("#4502 regression: main prompt builder no longer detects git status at build time")
	}
}
