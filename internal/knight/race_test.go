package knight

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestAnalyzeRecent_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	storeDir := filepath.Join(homeDir, ".ggcode", "sessions")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewJSONLStore(storeDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	// Create sessions with enough messages to pass the minimum threshold.
	for i := 0; i < 5; i++ {
		ses := session.NewSession("zai", "test", "test-model")
		store.AppendMessage(ses, provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "build the project"},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "running make build"},
				{Type: "tool_use", ToolName: "run_command", ToolID: "build"},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "you need to use make build"},
			},
		})
		store.AppendMessage(ses, provider.Message{
			Role: "assistant",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Understood"},
			},
		})
	}

	k := New(config.DefaultKnightConfig(), homeDir, projDir, store)

	// Run AnalyzeRecent concurrently from multiple goroutines to exercise
	// the analyzedSessions map under concurrent read/write.
	var wg sync.WaitGroup
	const concurrency = 6
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			analyzer := NewSessionAnalyzer(k)
			_, err := analyzer.AnalyzeRecent(context.Background())
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent AnalyzeRecent: %v", err)
	}
}
