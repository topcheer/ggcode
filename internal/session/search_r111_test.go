package session

import (
	"reflect"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// r111: cross-session search upgraded from a single whole-query substring
// needle to tokenized AND matching with double-quoted phrase support.
// Frontier grounding: LongMemEval-style multi-facet recall — real recall
// queries are multi-keyword ("oauth token refresh") and naive whole-query
// substring matching returns zero hits unless the terms appear consecutively.

func r111SeedStore(t *testing.T) *JSONLStore {
	t.Helper()
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ses := NewSession("zai", "default", "glm")
	ses.Title = "OAuth Work"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "How do I implement OAuth2 token refresh with rate limiting?"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Use a refresh token grant flow."}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
		t.Fatal(err)
	}

	other := NewSession("zai", "default", "glm")
	other.Title = "Unrelated"
	other.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Fix the CSS layout"}}},
	}
	if err := store.Save(other); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(other, other.Messages); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTokenizeQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{"single word", "OAuth", []string{"oauth"}},
		{"bare words lowercased", "OAuth Token REFRESH", []string{"oauth", "token", "refresh"}},
		{"collapses spaces", "  a \t b  ", []string{"a", "b"}},
		{"phrase kept whole", `refresh "token grant"`, []string{"refresh", "token grant"}},
		{"only phrase", `"rate limit"`, []string{"rate limit"}},
		{"unbalanced quote", `oauth "rate limit`, []string{"oauth", "rate limit"}},
		{"quotes only", `""`, nil},
		{"adjacent quotes form empty phrase", `x "" y`, []string{"x", "y"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenizeQuery(tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("tokenizeQuery(%q) = %#v, want %#v", tc.query, got, tc.want)
			}
		})
	}
}

// Multi-keyword queries that are NOT a consecutive substring must now match.
func TestSearchSessions_MultiTermAND(t *testing.T) {
	store := r111SeedStore(t)

	// "implement ... refresh ... rate" — all present in one block, never as
	// one consecutive substring of the query form used here.
	results, err := store.SearchSessions("implement refresh rate", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for multi-term AND, got %d", len(results))
	}

	// One term only exists in the other session → AND fails everywhere.
	results, err = store.SearchSessions("oauth css", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results when a term is absent, got %d", len(results))
	}
}

func TestSearchSessions_QuotedPhrase(t *testing.T) {
	store := r111SeedStore(t)

	// Phrase exists → hit.
	results, err := store.SearchSessions(`oauth "token refresh"`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for phrase query, got %d", len(results))
	}

	// Term split across words inside quotes → no hit (exact phrase required).
	results, err = store.SearchSessions(`oauth "refresh token grant"`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results when phrase does not appear verbatim, got %d", len(results))
	}
}

// A query that used to match as one consecutive substring must still match
// under AND semantics.
func TestSearchSessions_BackwardCompatibleSingleNeedle(t *testing.T) {
	store := r111SeedStore(t)

	for _, q := range []string{"OAuth2 token refresh", "refresh token grant flow", "OAuth"} {
		results, err := store.SearchSessions(q, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 {
			t.Errorf("query %q: expected 1 result, got %d", q, len(results))
		}
	}
}

// Quotes-only queries behave like empty queries instead of searching for
// literal quote characters.
func TestSearchSessions_QuotesOnlyIsEmpty(t *testing.T) {
	store := r111SeedStore(t)
	results, err := store.SearchSessions(`"" " "`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for quotes-only query, got %d", len(results))
	}
}

func TestMatchAllTerms_AnchorAndMissing(t *testing.T) {
	text := strings.ToLower("the refresh token grant flow")
	// Earliest term anchors the snippet.
	idx, tlen := matchAllTerms(text, []string{"grant", "refresh"})
	if idx != 4 || tlen != len("refresh") {
		t.Errorf("matchAllTerms anchor = (%d,%d), want (4,%d)", idx, tlen, len("refresh"))
	}
	// Missing term → no match.
	if idx, _ := matchAllTerms(text, []string{"refresh", "absent"}); idx != -1 {
		t.Errorf("missing term should yield -1, got %d", idx)
	}
	if idx, tlen := matchAllTerms(text, []string{"the"}); idx != 0 || tlen != 3 {
		t.Errorf("single term = (%d,%d), want (0,3)", idx, tlen)
	}
}

// The snippet must be anchored on the earliest term, not on a later one.
func TestSearchSessions_SnippetAnchoredOnEarliestTerm(t *testing.T) {
	store := r111SeedStore(t)
	results, err := store.SearchSessions("refresh implement", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// "implement" occurs at index 12, "refresh" later; snippet should
	// start near "implement...".
	if !strings.Contains(results[0].Snippet, "implement") {
		t.Errorf("snippet not anchored on earliest term: %q", results[0].Snippet)
	}
}
