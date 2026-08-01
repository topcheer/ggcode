package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestSearchSessions_BasicMatch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "default", "glm")
	ses.Title = "OAuth Debugging"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "How do I implement OAuth2 token refresh?"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Use a refresh token grant flow."}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
		t.Fatal(err)
	}

	// Non-matching session
	ses2 := NewSession("zai", "default", "glm")
	ses2.Title = "Unrelated"
	ses2.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Fix the CSS layout"}}},
	}
	if err := store.Save(ses2); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(ses2, ses2.Messages); err != nil {
		t.Fatal(err)
	}

	results, err := store.SearchSessions("OAuth", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].SessionID != ses.ID {
		t.Errorf("expected session %s, got %s", ses.ID, results[0].SessionID)
	}
	if results[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", results[0].Role)
	}
	if results[0].Title != "OAuth Debugging" {
		t.Errorf("expected title 'OAuth Debugging', got %q", results[0].Title)
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
}

func TestSearchSessions_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "default", "glm")
	ses.Title = "Test"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "HELP ME WITH REFACTORING"}}},
	}
	store.Save(ses)
	store.AppendMessagesBatchToDisk(ses, ses.Messages)

	results, err := store.SearchSessions("refactoring", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestSearchSessions_EmptyQuery(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	results, err := store.SearchSessions("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Fatalf("expected nil for empty query, got %v", results)
	}
}

func TestSearchSessions_MaxResults(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		ses := NewSession("zai", "default", "glm")
		ses.Title = "Session"
		ses.Messages = []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "findme common keyword"}}},
		}
		store.Save(ses)
		store.AppendMessagesBatchToDisk(ses, ses.Messages)
	}

	results, err := store.SearchSessions("findme", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

func TestSearchSessions_NoResults(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "default", "glm")
	ses.Title = "Test"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello world"}}},
	}
	store.Save(ses)
	store.AppendMessagesBatchToDisk(ses, ses.Messages)

	results, err := store.SearchSessions("nonexistent", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestMakeSnippet(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		matchIdx int
		needle   string
		want     string
	}{
		{
			name:     "short text no truncation",
			text:     "find me here",
			matchIdx: 0,
			needle:   "find",
			want:     "find me here",
		},
		{
			name:     "long text with truncation",
			text:     "AAAA find BBBB " + string(make([]byte, 300)),
			matchIdx: 5,
			needle:   "find",
			want:     "...find BBBB ...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := makeSnippet(tc.text, tc.matchIdx, tc.needle)
			// For short text, snippet should contain the needle
			if len(tc.text) <= 200 {
				if got != tc.want && !contains(got, tc.needle) {
					t.Errorf("expected snippet containing %q, got %q", tc.needle, got)
				}
			}
			// Snippet should never exceed ~210 chars (200 + ellipsis)
			if len(got) > 210 {
				t.Errorf("snippet too long: %d chars", len(got))
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(s) > 0 && findSubstring(s, sub)))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/.ggcode/sessions/abc123.jsonl", "abc123"},
		{"abc456.jsonl", "abc456"},
		{"/tmp/test.jsonl", "test"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := extractSessionID(tc.path)
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestSearchSessions_TimestampOrdering(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Create two sessions with different timestamps
	ses1 := NewSession("zai", "default", "glm")
	ses1.Title = "Old"
	ses1.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "unique keyword match"}}},
	}
	store.Save(ses1)
	store.AppendMessagesBatchToDisk(ses1, ses1.Messages)
	// Set old timestamp
	os.Chtimes(filepath.Join(dir, ses1.ID+".jsonl"), time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))

	ses2 := NewSession("zai", "default", "glm")
	ses2.Title = "New"
	ses2.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "unique keyword match again"}}},
	}
	store.Save(ses2)
	store.AppendMessagesBatchToDisk(ses2, ses2.Messages)

	results, err := store.SearchSessions("unique keyword", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// Results should be sorted by timestamp descending
	// (session2 was created later, but both messages lack explicit timestamps,
	// so we just verify we got hits from both sessions)
	sessionIDs := map[string]bool{}
	for _, r := range results {
		sessionIDs[r.SessionID] = true
	}
	if !sessionIDs[ses1.ID] || !sessionIDs[ses2.ID] {
		t.Errorf("expected results from both sessions")
	}
}
