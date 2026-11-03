package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestLoadWithOptions_ReleasesStoreLock(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	id := "lock-release-session"
	path := filepath.Join(dir, id+".jsonl")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(jsonlRecord{Type: "meta", SessionID: id, Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode(jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:      "m1",
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: "hi"}},
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := store.LoadWithOptions(id, true); err != nil {
		t.Fatalf("LoadWithOptions: %v", err)
	}

	done := make(chan struct{})
	go func() {
		store.SetFullLoad(false)
		store.SetFullLoad(true)
		close(done)
	}()
	select {
	case <-done:

	case <-time.After(2 * time.Second):
		t.Fatal("LoadWithOptions left the store mutex locked (#531 Bug A)")
	}

	if _, err := store.Load(id); err != nil {
		t.Fatalf("Load after LoadWithOptions: %v", err)
	}
}

func TestEnsureMeta_DoesNotTruncateExistingFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	storeA, _ := NewJSONLStore(dir)
	id := "toctou-session"
	path := filepath.Join(dir, id+".jsonl")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(jsonlRecord{Type: "meta", SessionID: id, Title: "B"}); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode(jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:      "b-msg-1",
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: "from process B"}},
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ses := NewSession("test", "test", "test")
	ses.ID = id
	ses.Messages = []provider.Message{{
		ID:      "a-msg",
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "user text"}},
	}}

	if err := storeA.EnsureMeta(ses); err != nil {
		t.Fatalf("EnsureMeta on existing file: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines int
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 2 {
		t.Fatalf("EnsureMeta truncated the session file: expected 2 lines (meta + message), got %d", lines)
	}
	loaded, err := storeA.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].ID != "b-msg-1" {
		t.Fatalf("process B's message was lost: got %v", loaded.Messages)
	}
}

func TestEnsureMeta_ConcurrentStoresNoTruncation(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	storeA, _ := NewJSONLStore(dir)
	storeB, _ := NewJSONLStore(dir)
	id := "concurrent-toctou"

	ses := NewSession("test", "test", "test")
	ses.ID = id
	ses.Messages = []provider.Message{{
		ID:      "a-msg",
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "user text"}},
	}}

	if err := storeA.EnsureMeta(ses); err != nil {
		t.Fatal(err)
	}

	const iterations = 200
	appended := make([]*provider.Message, 0, iterations)
	for i := 0; i < iterations; i++ {
		msg := &provider.Message{
			ID:      fmt.Sprintf("b-msg-%d", i),
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("append %d", i)}},
		}
		appended = append(appended, msg)

		bSes := &Session{ID: id, Title: ses.Title, Workspace: ses.Workspace}
		bSes.Messages = []provider.Message{{
			ID:      "b-user",
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: "user text"}},
		}}
		if err := storeB.AppendMessageToDisk(bSes, *msg); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}

		if err := storeA.EnsureMeta(ses); err != nil {
			t.Fatalf("EnsureMeta %d: %v", i, err)
		}
	}

	loaded, err := storeA.LoadWithOptions(id, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != iterations {
		t.Fatalf("truncation detected: expected %d messages, got %d", iterations, len(loaded.Messages))
	}
	if loaded.Messages[0].ID != "b-msg-0" || loaded.Messages[iterations-1].ID != fmt.Sprintf("b-msg-%d", iterations-1) {
		t.Fatalf("unexpected message range: first=%s last=%s", loaded.Messages[0].ID, loaded.Messages[iterations-1].ID)
	}
}

func writeWindowBoundaryJSONL(t *testing.T, dir, id string) (exactBoundaryID string) {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)

	now := time.Now()
	last := now.Add(-1 * time.Hour)
	cutoff := last.Add(-RecentMessageWindow)

	for i := 0; i < recentMessageThreshold-2; i++ {
		rec := jsonlRecord{
			Type: "message",
			Message: &provider.Message{
				ID:      fmt.Sprintf("old-%d", i),
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("old %d", i)}},
			},
			Timestamp: now.Add(-48 * time.Hour).Add(time.Duration(i) * time.Second),
		}
		if err := enc.Encode(rec); err != nil {
			t.Fatal(err)
		}
	}

	victim := jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:      "victim-boundary",
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: "just outside window"}},
		},
		Timestamp: cutoff.Add(-time.Second),
	}
	if err := enc.Encode(victim); err != nil {
		t.Fatal(err)
	}

	exact := jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:      "exact-24h",
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "exactly 24h before last"}},
		},
		Timestamp: cutoff,
	}
	if err := enc.Encode(exact); err != nil {
		t.Fatal(err)
	}

	final := jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:      "final-msg",
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "newest"}},
		},
		Timestamp: last,
	}
	if err := enc.Encode(final); err != nil {
		t.Fatal(err)
	}
	return "exact-24h"
}

func TestWindowedLoad_BoundaryOffsets(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	id := "window-boundary"
	exactID := writeWindowBoundaryJSONL(t, dir, id)

	loaded, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.Messages) != 2 {
		ids := make([]string, 0, len(loaded.Messages))
		for _, m := range loaded.Messages {
			ids = append(ids, m.ID)
		}
		t.Fatalf("expected 2 in-window messages, got %d: %v", len(loaded.Messages), ids)
	}
	if loaded.Messages[0].ID != exactID {
		t.Errorf("first in-window message should be the exactly-24h message %q, got %q", exactID, loaded.Messages[0].ID)
	}
	for _, m := range loaded.Messages {
		if m.ID == "victim-boundary" {
			t.Error("message outside the window whose line-end offset equals msgCutoff was retained (#531 Bug D)")
		}
	}

	full, err := store.LoadWithOptions(id, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Messages) != recentMessageThreshold+1 {
		t.Fatalf("full load expected %d messages, got %d", recentMessageThreshold+1, len(full.Messages))
	}
}
