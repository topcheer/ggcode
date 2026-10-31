package session

import (
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4717: the streaming usage writer mutates TokenUsage/UpdatedAt while
// /tag //untag //pin's appendMetaLocked serializes the same fields - two
// different locks, no exclusion, a -race-detectable tear that could
// persist a torn usage record. WithSessionLocked puts both sides under
// s.mu. Run with -race for the detector verdict.
func TestIssue4717UsageWritesExcludeMetaSerialization(t *testing.T) {
	store := &JSONLStore{dir: t.TempDir(), fullLoad: true}
	ses := &Session{
		ID:        "issue-4717-race",
		UpdatedAt: time.Now(),
		// persistEligible gate: a real user message.
		Messages: []provider.Message{{
			ID:      "m1",
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: "hi"}},
		}},
	}

	const rounds = 100
	var wg sync.WaitGroup
	wg.Add(2)
	// Side A: the usage writer (recordSessionUsage pattern) - now under
	// the store lock via WithSessionLocked.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			store.WithSessionLocked(ses, func() {
				ses.TokenUsage = ses.TokenUsage.Add(provider.TokenUsage{InputTokens: 1})
				ses.UpdatedAt = time.Now()
			})
		}
	}()
	// Side B: the /tag slash-command pattern - SetTags -> appendMetaLocked
	// serializes TokenUsage/UpdatedAt under s.mu.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := store.SetTags(ses, []string{"race"}); err != nil {
				t.Errorf("SetTags: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	if got := ses.TokenUsage.InputTokens; got != rounds {
		t.Fatalf("usage writes lost under contention: got %d, want %d", got, rounds)
	}
}
