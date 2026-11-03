package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue4356UsagePersistsWhenMemoryWindowDroppedUserMessages(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := &Session{ID: "s4356", Title: "t", CreatedAt: time.Now().Add(-48 * time.Hour), UpdatedAt: time.Now().Add(-48 * time.Hour)}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageToDisk(ses, provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old"}}}); err != nil {
		t.Fatal(err)
	}

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

	light := &Session{ID: "s4356", Title: "t"}
	if light.HasUserInteraction() {
		t.Fatalf("precondition broken: empty session claims interaction")
	}

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

	ses := &Session{ID: "ghost4356", Title: "g"}
	if err := store.EnsureMeta(ses); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ghost4356.jsonl")); !os.IsNotExist(err) {
		t.Fatal("EnsureMeta created a meta/file for a never-interacted session")
	}
}
