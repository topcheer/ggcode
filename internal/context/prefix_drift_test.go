package context

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestPrefixDriftAnchorGate: instrumentation is OFF by default and ON only
// while the anchor file ~/.ggcode/prefixdrifting exists. A head rewrite
// under gate-open must land a JSON record with block fingerprints and the
// first-divergence line; with no anchor, nothing is written.
func TestPrefixDriftAnchorGate(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	anchor := filepath.Join(home, ".ggcode", prefixDriftAnchorFile)
	dir := filepath.Join(home, ".ggcode", prefixDriftDir)
	cleanup := func() {
		_ = os.Remove(anchor)
		_ = os.RemoveAll(dir)
	}
	cleanup()
	defer cleanup()

	reset := func() {
		prefixDriftMu.Lock()
		if prefixDriftFile != nil {
			_ = prefixDriftFile.Close()
		}
		prefixDriftFile = nil
		prefixDriftPath = ""
		prefixDriftEnabled = false
		prefixDriftLastCheck = time.Time{}
		prefixDriftMu.Unlock()
	}

	sysMsg := func(text string) provider.Message {
		return provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
	}

	// 1) OFF by default: rewrite with no anchor writes nothing.
	m := newTestManager(t)
	m.UpdateFirstSystemMessage(sysMsg("line-one\nline-two\nline-three"))
	m.UpdateFirstSystemMessage(sysMsg("line-one\nline-CHANGED\nline-three"))
	reset()
	if _, err := os.Stat(dir); err == nil {
		entries, _ := os.ReadDir(dir)
		if len(entries) > 0 {
			t.Fatalf("drift records written while disabled: %d files", len(entries))
		}
	}

	// 2) ON with anchor: rewrite lands a pinpointing record.
	if err := os.MkdirAll(filepath.Dir(anchor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(anchor, []byte("on"), 0o644); err != nil {
		t.Fatal(err)
	}
	reset()

	m2 := newTestManager(t)
	m2.UpdateFirstSystemMessage(sysMsg("line-one\nline-two\nline-three"))
	m2.UpdateFirstSystemMessage(sysMsg("line-one\nline-CHANGED\nline-three"))

	path := prefixDriftActivePath()
	if path == "" {
		t.Fatal("no drift sink opened under anchor gate")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read drift sink: %v", err)
	}
	lines := nonEmptyLines(string(raw))
	if len(lines) == 0 {
		t.Fatal("no drift records written under anchor gate")
	}
	var rec prefixDriftRecord
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("record not valid JSON: %v", err)
	}
	if rec.Event != "rewrite" {
		t.Fatalf("expected event=rewrite, got %s", rec.Event)
	}
	if rec.FirstDiffLine != 2 {
		t.Fatalf("first divergence should be line 2, got %d", rec.FirstDiffLine)
	}
	if len(rec.OldBlocks) != 1 || len(rec.NewBlocks) != 1 {
		t.Fatalf("expected 1+1 block fingerprints, got %d+%d", len(rec.OldBlocks), len(rec.NewBlocks))
	}
	if rec.OldBlocks[0].Sha16 == rec.NewBlocks[0].Sha16 {
		t.Fatal("block hashes must differ for drifted head")
	}
	if rec.DiffOld == "" || rec.DiffNew == "" {
		t.Fatal("diff context snippets missing")
	}
}

// 3) insert path also records when enabled.
func TestPrefixDriftInsert(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	anchor := filepath.Join(home, ".ggcode", prefixDriftAnchorFile)
	dir := filepath.Join(home, ".ggcode", prefixDriftDir)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Dir(anchor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(anchor, []byte("on"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(anchor)
	defer os.RemoveAll(dir)

	prefixDriftMu.Lock()
	if prefixDriftFile != nil {
		_ = prefixDriftFile.Close()
	}
	prefixDriftFile = nil
	prefixDriftPath = ""
	prefixDriftEnabled = false
	prefixDriftLastCheck = time.Time{}
	prefixDriftMu.Unlock()

	m := newTestManager(t)
	m.UpdateFirstSystemMessage(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "fresh head"}}})

	path := prefixDriftActivePath()
	if path == "" {
		t.Fatal("insert under anchor gate should open the sink")
	}
	raw, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(raw))
	if len(lines) == 0 {
		t.Fatal("insert should leave a drift record")
	}
	var rec prefixDriftRecord
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &rec)
	if rec.Event != "insert" {
		t.Fatalf("expected event=insert, got %s", rec.Event)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
