package tool

// #3163 probes: O_EXCL placeholder closes the Stat→AtomicWriteFile TOCTOU
// window in create_skill. The placeholder is the atomic cross-process gate —
// a concurrent creator must be rejected even when the file it would collide
// with is a 0-byte mid-race placeholder, and the write-failure path must not
// leave that placeholder behind blocking re-creation.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func issue3163Create(t *testing.T, dir, name string) Result {
	t.Helper()
	tool := &CreateSkillTool{WorkingDir: dir}
	input, err := json.Marshal(map[string]string{
		"name":        name,
		"description": "probe skill",
		"content":     "body text",
		"scope":       "project",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

// A 0-byte placeholder (the exact mid-race state of a concurrent creator
// between EXCL success and rename completion) must be rejected — this is the
// state the pre-fix Stat check also caught, but EXCL must keep catching it
// when it appears AFTER our own Stat ran.
func TestIssue3163_ZeroBytePlaceholderBlocksCreate(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "dup")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res := issue3163Create(t, dir, "dup")
	if !res.IsError {
		t.Fatalf("expected rejection against 0-byte placeholder, got: %v", res.Content)
	}
	if !strings.Contains(res.Content, "already exists") {
		t.Fatalf("error should say already exists, got: %v", res.Content)
	}
}

// Two sequential EXCL opens on the same path: the second must fail with
// EEXIST — the kernel-level guarantee the fix relies on (also CREATE_NEW on
// Windows via Go's os.OpenFile mapping).
func TestIssue3163_SecondEXCLFails(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "gate")
	f1, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("first EXCL should succeed: %v", err)
	}
	f1.Close()
	_, err = os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if !os.IsExist(err) {
		t.Fatalf("second EXCL must fail with EEXIST, got: %v", err)
	}
}

// Normal creation still succeeds and the final file carries full content
// (the placeholder is replaced by the rename, never shipped as-is).
func TestIssue3163_CreateStillSucceedsWithContent(t *testing.T) {
	dir := t.TempDir()
	res := issue3163Create(t, dir, "fresh")
	if res.IsError {
		t.Fatalf("fresh create should succeed, got: %v", res.Content)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".ggcode", "skills", "fresh", "SKILL.md"))
	if err != nil {
		t.Fatalf("skill file missing: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("final file must not be the empty placeholder")
	}
	if !strings.Contains(string(data), "body text") {
		t.Fatal("final file must carry the requested content")
	}
}
