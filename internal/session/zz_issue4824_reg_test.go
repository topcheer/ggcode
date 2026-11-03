package session

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue4824_CreationWindowRegistryLockFreeRepairRace(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			ses := &Session{ID: fmt.Sprintf("race-%06d", i), Title: "t", Workspace: "/ws"}
			ses.CreatedAt = time.Now()
			ses.UpdatedAt = time.Now()
			_ = s.Save(ses)

			if i%7 == 0 {
				s.mu.Lock()
				s.createdPendingMu.Lock()
				for k, v := range s.createdPending {
					s.createdPending[k] = v.Add(-2 * sessionCreationWindow)
				}
				s.createdPendingMu.Unlock()
				s.mu.Unlock()
			}
		}
	}()

	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for j := 0; ; j++ {
				select {
				case <-stop:
					return
				default:
				}
				id := fmt.Sprintf("race-%06d", (seed*997+j)%(j+1+seed))
				_ = s.inCreationWindow(id)
			}
		}(g)
	}

	time.Sleep(400 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestIssue4824_RegistryBatchClearConcurrent(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewJSONLStore(dir)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				ses := &Session{ID: fmt.Sprintf("bc-%d-%d", g, i), Workspace: "/ws"}
				ses.CreatedAt = time.Now()
				ses.UpdatedAt = time.Now()
				_ = s.Save(ses)
				_ = s.AppendMessagesBatchToDisk(ses, []provider.Message{
					{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}},
				})
			}
		}(g)
	}
	wg.Wait()
}
