package session

import (
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue4717UsageWritesExcludeMetaSerialization(t *testing.T) {
	store := &JSONLStore{dir: t.TempDir(), fullLoad: true}
	ses := &Session{
		ID:        "issue-4717-race",
		UpdatedAt: time.Now(),

		Messages: []provider.Message{{
			ID:      "m1",
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: "hi"}},
		}},
	}

	const rounds = 100
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			store.WithSessionLocked(ses, func() {
				ses.TokenUsage = ses.TokenUsage.Add(provider.TokenUsage{InputTokens: 1})
				ses.UpdatedAt = time.Now()
			})
		}
	}()

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
