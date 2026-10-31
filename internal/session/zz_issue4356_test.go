package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4356 regression: usage/metric/meta persistence gates used the cheap
// in-memory HasUserInteraction alone. Time-windowed loading (fullLoad=false)
// drops user messages older than RecentMessageWindow from ses.Messages, so
// for an old-but-interacted session every gate silently returned nil and
// pin/tag/model/usage changes were lost across restarts. The gates now
// consult the on-disk file via persistEligible.

func TestIssue4356UsagePersistsWhenMemoryWindowDroppedUserMessages(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Build a real interacted session through the canonical API, then
	// backdate its user message beyond RecentMessageWindow so a fresh,
	// time-windowed load yields an in-memory Messages slice with NO user
	// turns - exactly the regression precondition.
	ses := &Session{ID: "s4356", Title: "t", CreatedAt: time.Now().Add(-48 * time.Hour), UpdatedAt: time.Now().Add(-48 * time.Hour)}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageToDisk(ses, provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old"}}}); err != nil {
		t.Fatal(err)
	}
	// Anchor the window at NOW with a fresh assistant record, then backdate
	// ONLY the user record (line surgery): windowed load (cutoff anchored on
	// the newest record) drops the old user message from memory while the
	// window-free on-disk scan still finds it.
	if err := store.AppendMessageToDisk(ses, provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "recent"}}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s4356.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for i, ln := range lines {
		if strings.Contains(ln, `"role":"user"`) {
			lines[i] = strings.ReplaceAll(ln,
				`"timestamp":"`+time.Now().Format("2006"), `"timestamp":"2000`)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	// Regression precondition, constructed directly (the windowed loader's
	// filter internals are not under test): an in-memory session whose
	// Messages slice carries no user turn - what a time-windowed load of an
	// old-but-interacted session yields.
	light := &Session{ID: "s4356", Title: "t"}
	if light.HasUserInteraction() {
		t.Fatalf("precondition broken: empty session claims interaction")
	}

	// The fix: usage persistence must still land because the FILE proves
	// interaction (persistEligible on-disk fallback).
	light.UpdatedAt = time.Now()
	if err := store.AppendUsageEntry(light, UsageEntry{Timestamp: time.Now(), Model: "m", Usage: provider.TokenUsage{InputTokens: 1, OutputTokens: 1}}); err != nil {
		t.Fatal(err)
	}
	raw2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw2), `"usage"`) {
		t.Fatal("#4356 regression: usage entry silently dropped for window-loaded interacted session")
	}
}

func TestIssue4356EnsureMetaDoesNotCreateFileForNeverInteractedSession(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// No session file, no interaction: EnsureMeta must stay a no-op (the
	// on-disk fallback must not fabricate eligibility from absence).
	ses := &Session{ID: "ghost4356", Title: "g"}
	if err := store.EnsureMeta(ses); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ghost4356.jsonl")); !os.IsNotExist(err) {
		t.Fatal("EnsureMeta created a meta/file for a never-interacted session")
	}
}
