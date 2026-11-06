package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #5136: #5118 fixed only the write side. Pre-fix trajectory lines and
// project auto-memory insight entries still carry the raw user prompt at
// rest (and the global store even propagates it across projects via merge).
// The load path must redact in-memory AND cure the at-rest file.

const rawPrompt = "fix the deploy script, token sk-abc123secretkeydoNotLeak please"

func seedPreFixTraj(t *testing.T, path string) {
	t.Helper()
	l := trajectoryLearning{
		Task:    rawPrompt,
		Insight: "deploy scripts need token rotation checks",
	}
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIssue5136LoadTrajRedactsAndCuresFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trajectory-learnings.jsonl")
	seedPreFixTraj(t, path)

	entries, err := loadTrajFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if strings.Contains(entries[0].Task, "sk-abc123") {
		t.Errorf("loaded entry still carries raw prompt: %q", entries[0].Task)
	}
	if !strings.Contains(entries[0].Task, "[REDACTED") {
		t.Errorf("loaded entry Task should be redacted, got %q", entries[0].Task)
	}

	// At-rest cure: the file itself must no longer contain the raw key.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-abc123") {
		t.Errorf("#5136: at-rest file not cured, raw key still present")
	}
	// Insight content must be untouched by the Task-only redaction.
	if !strings.Contains(entries[0].Insight, "token rotation") {
		t.Errorf("insight must be preserved, got %q", entries[0].Insight)
	}
}

func TestIssue5136MergeIntakeRedacts(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	seedPreFixTraj(t, filepath.Join(srcDir, "trajectory-learnings.jsonl"))

	n, err := TrajMergeInto(dstDir, filepath.Join(srcDir, "trajectory-learnings.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 merged entry, got %d", n)
	}
	data, err := os.ReadFile(filepath.Join(dstDir, ".ggcode", "trajectory-learnings.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-abc123") {
		t.Errorf("#5136: merge propagated raw prompt into destination store")
	}
}

func TestIssue5136MemoryScrub(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".ggcode", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	insight := "## Run Insights\n\nTask: " + rawPrompt + "\n\n[verified: yes]\n"
	if err := os.WriteFile(filepath.Join(memDir, "insights-entry.md"), []byte(insight), 0o644); err != nil {
		t.Fatal(err)
	}
	clean := "prefer table-driven tests\n"
	if err := os.WriteFile(filepath.Join(memDir, "clean-entry.md"), []byte(clean), 0o644); err != nil {
		t.Fatal(err)
	}

	ScrubPromptLeakedMemoryEntries(dir)

	scrubbed, err := os.ReadFile(filepath.Join(memDir, "insights-entry.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(scrubbed), "sk-abc123") {
		t.Errorf("#5136: memory entry still carries raw prompt")
	}
	if !strings.Contains(string(scrubbed), "[REDACTED") {
		t.Errorf("memory entry should be redacted, got %q", string(scrubbed))
	}

	// Clean entry must be byte-identical (read-only check, no churn).
	after, err := os.ReadFile(filepath.Join(memDir, "clean-entry.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != clean {
		t.Errorf("clean entry must not be touched, got %q", string(after))
	}
}
