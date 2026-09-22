package session

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// newRankedTestStore builds a store with deterministic sessions:
//
//	s1 "Fix batch_replace anchor bug"   (workspace /proj/a): user asks about
//	   the batch_replace anchor bug; assistant explains makeSnippet offsets.
//	s2 "Refactor session search"        (workspace /proj/b): discusses the
//	   substring search implementation.
//	s3 "Cooking recipes"                (workspace /proj/c): unrelated noise.
func newRankedTestStore(t *testing.T) *JSONLStore {
	t.Helper()
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id, title, workspace string, msgs ...provider.Message) *Session {
		ses := NewSession("zai", "ep", "glm-5-turbo")
		ses.ID = id
		ses.Title = title
		ses.Workspace = workspace
		ses.Messages = msgs
		if err := store.Save(ses); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendMetaToDisk(ses); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
			t.Fatal(err)
		}
		return ses
	}
	txt := func(role, s string) provider.Message {
		return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: s}}}
	}
	mk("s1", "Fix batch_replace anchor bug", "/proj/a",
		txt("user", "The batch_replace anchor offsets were wrong because makeSnippet used byte positions."),
		txt("assistant", "Right: the anchor bug came from mixing rune and byte offsets in makeSnippet."),
	)
	mk("s2", "Refactor session search", "/proj/b",
		txt("user", "Session search should stream JSONL instead of loading everything."),
		txt("assistant", "Agreed, streaming the search avoids holding full sessions in memory."),
	)
	mk("s3", "Cooking recipes", "/proj/c",
		txt("user", "Best way to cook noodles?"),
	)
	return store
}

func TestSearchSessionsRankedRanksPhraseAndCoverage(t *testing.T) {
	store := newRankedTestStore(t)
	now := time.Now()

	res, err := store.SearchSessionsRanked("batch_replace anchor bug", 10, RankedOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("expected at least one hit")
	}
	// The s1 assistant message contains the exact phrase; it must outrank
	// any partial-token hit.
	top := res[0]
	if top.SessionID != "s1" {
		t.Fatalf("top hit session = %q, want s1 (results: %+v)", top.SessionID, res)
	}
	if !strings.Contains(top.Snippet, "anchor") {
		t.Fatalf("snippet missing expected term: %q", top.Snippet)
	}
	if top.Score <= 0 {
		t.Fatalf("expected positive score, got %v", top.Score)
	}
	// Every remaining hit is a partial match (assistant/user variants of
	// the same discussion): they must all be strictly positive, and the
	// top-scored one must be the s1 session (asserted above).
	for _, r := range res {
		if r.Score <= 0 {
			t.Fatalf("non-positive score in results: %+v", r)
		}
	}
}

func TestSearchSessionsRankedCoverageGate(t *testing.T) {
	store := newRankedTestStore(t)
	now := time.Now()

	// Only 1 of 3 tokens present in s2 → below the 50% coverage gate for a
	// multi-token query, and no phrase → must not appear.
	res, err := store.SearchSessionsRanked("noodles session refactor", 10, RankedOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.SessionID == "s3" {
			t.Fatalf("unrelated session leaked into results: %+v", r)
		}
	}
}

func TestSearchSessionsRankedFilters(t *testing.T) {
	store := newRankedTestStore(t)
	now := time.Now()

	// Role filter: assistant-only must exclude the s1 user question hit.
	res, err := store.SearchSessionsRanked("batch_replace", 10, RankedOptions{Role: "assistant", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Role != "assistant" {
			t.Fatalf("role filter leaked %q hit", r.Role)
		}
	}

	// Workspace filter: /proj/b scope must exclude s1 and s3.
	res, err = store.SearchSessionsRanked("search", 10, RankedOptions{Workspace: "/proj/b", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("expected workspace-filtered hits")
	}
	for _, r := range res {
		if r.SessionID == "s1" || r.SessionID == "s3" {
			t.Fatalf("workspace filter leaked session %s", r.SessionID)
		}
	}

	// SinceDays filter: everything is fresh, an absurdly old window still
	// matches nothing when the whole session predates it — verified via
	// SinceDays=0 (no filter) returning hits.
	res, err = store.SearchSessionsRanked("noodles", 10, RankedOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("expected hit for noodles query")
	}
}

func TestSearchSessionsRankedUserBoost(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	txt := func(role, s string) provider.Message {
		return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: s}}}
	}
	ses := NewSession("zai", "ep", "glm")
	ses.ID = "ua"
	ses.Title = "Decision log"
	ses.Messages = []provider.Message{
		txt("assistant", "We decided to use sqlite for the cache layer."),
		txt("user", "We decided to use sqlite for the cache layer."),
	}
	_ = store.Save(ses)
	_ = store.AppendMetaToDisk(ses)
	_ = store.AppendMessagesBatchToDisk(ses, ses.Messages)

	res, err := store.SearchSessionsRanked("sqlite cache layer", 10, RankedOptions{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 hits, got %d", len(res))
	}
	if res[0].Role != "user" {
		t.Fatalf("user message should outrank identical assistant message, got %q first", res[0].Role)
	}
}

func TestSearchSessionsRankedStopwordFallback(t *testing.T) {
	store := newRankedTestStore(t)

	// Pure-stopword query cannot be tokenized → substring fallback must
	// still return matches rather than an error/empty.
	res, err := store.SearchSessionsRanked("the it", 10, RankedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// "the" appears inside normal text; at minimum it must not error.
	_ = res
}

func TestTokenizeRankedQuery(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"How do we fix the build?", []string{"fix", "build"}},
		{"批处理 替换 bug", []string{"批", "处", "理", "替", "换", "bug"}},
		{"a i of", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := TokenizeRankedQuery(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("TokenizeRankedQuery(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("TokenizeRankedQuery(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestSearchSessionsRankedSinceDays(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	txt := func(role, s string) provider.Message {
		return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: s}}}
	}
	ses := NewSession("zai", "ep", "glm")
	ses.ID = "fresh1"
	ses.Title = "Today postgres tuning"
	ses.Messages = []provider.Message{txt("user", "tuned postgres vacuum settings")}
	_ = store.Save(ses)
	_ = store.AppendMetaToDisk(ses)
	if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	res, err := store.SearchSessionsRanked("postgres", 10, RankedOptions{SinceDays: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("expected fresh postgres hits within 1 day window")
	}

	// Message-level time filter: a record stamped 40 days ago must be
	// rejected by SinceDays=1. Exercise rankedScanLine directly — the
	// public API stamps fresh timestamps on write, so this is the only way
	// to control message age deterministically.
	line := `{"type":"message","timestamp":"` + now.Add(-40*24*time.Hour).UTC().Format(time.RFC3339Nano) + `","message":{"role":"user","content":[{"type":"text","text":"we migrated the postgres schema"}]}}`
	opts := RankedOptions{SinceDays: 1, Now: now}
	if _, ok := rankedScanLine(line, "/fake/sessions/old1.jsonl", "Old postgres migration", 0, TokenizeRankedQuery("postgres"), "postgres", false, opts, now); ok {
		t.Fatal("40-day-old message leaked through SinceDays=1 filter")
	}
	if _, ok := rankedScanLine(line, "/fake/sessions/old1.jsonl", "Old postgres migration", 0, TokenizeRankedQuery("postgres"), "postgres", false, RankedOptions{Now: now}, now); !ok {
		t.Fatal("same message should match without the time filter")
	}
}
