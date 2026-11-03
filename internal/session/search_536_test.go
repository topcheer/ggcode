package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestSearchSessions_GlobalSortBeforeTruncate(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	msg := func(text string) provider.Message {
		return provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
	}

	first := NewSession("zai", "default", "glm")
	first.Title = "recent session, old hit"
	first.UpdatedAt = now
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(first, []provider.Message{msg("needle hit")}); err != nil {
		t.Fatal(err)
	}

	oldTS := now.Add(-2 * time.Hour)
	if err := rewriteMessageTimestamps(filepath.Join(dir, first.ID+".jsonl"), oldTS); err != nil {
		t.Fatal(err)
	}

	second := NewSession("zai", "default", "glm")
	second.Title = "older session, new hit"
	second.UpdatedAt = now.Add(-1 * time.Hour)
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(second, []provider.Message{msg("needle hit")}); err != nil {
		t.Fatal(err)
	}

	results, err := store.SearchSessions("needle", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].SessionID != second.ID {
		t.Errorf("expected newest hit from session %s, got session %s (old hit leaked through truncation)",
			second.ID, results[0].SessionID)
	}

	all, err := store.SearchSessions("needle", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 results, got %d", len(all))
	}
	if all[0].SessionID != second.ID || all[1].SessionID != first.ID {
		t.Errorf("expected [newest=%s, oldest=%s], got [%s, %s]",
			second.ID, first.ID, all[0].SessionID, all[1].SessionID)
	}
	if !all[1].Timestamp.Equal(oldTS) {
		t.Errorf("expected rewritten timestamp %v, got %v", oldTS, all[1].Timestamp)
	}
}

func rewriteMessageTimestamps(path string, ts time.Time) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return err
		}
		if rec["type"] == "message" {
			rec["timestamp"] = ts.Format(time.RFC3339Nano)
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		out = append(out, string(b))
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0600)
}
