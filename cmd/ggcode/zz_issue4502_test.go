//go:build goolm

package main

import (
	"os"
	"strings"
	"testing"
)

// #4502 pin: the main system-prompt builder must detect git status at
// BUILD time, not reuse the startup snapshot - the old closure captured
// the one-shot gitStatus variable and every refresh (save_memory, knight
// reload, skills ticker) re-injected stale branch/dirty state.
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
