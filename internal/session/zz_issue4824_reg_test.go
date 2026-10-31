package session

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4829 regression pin for #4824: the creation-window registry is read by
// the prune path, which also runs via repairIndexOutsideLock - the lock-FREE
// repair variant (List drops s.mu around repairIndex, see #4353). Save()
// registers the window under s.mu. Without the registry's own lock the two
// race (CI fatal: "concurrent map read and map write" at inCreationWindow).
// Run with -race; the pre-fix map access is flagged within a few iterations.
func TestIssue4824_CreationWindowRegistryLockFreeRepairRace(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer: Save() registers creation-window entries (under s.mu).
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
			// Rotate ids so entries accumulate and the reader has live
			// map keys to probe; also exercise the window-expiry delete.
			// Aging must respect the registry's own lock (s.mu ->
			// createdPendingMu order, same as the store's writers).
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

	// Reader: the lock-free repair path (what List drives) probes
	// inCreationWindow for every index entry WITHOUT holding s.mu. Pre-fix
	// this is a bare map read racing the writer above.
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

// Same-package smoke: the batch-append clear (third access site) rides the
// same dedicated mutex - concurrent Save + AppendMessagesBatchToDisk on
// distinct sessions must not race on the registry.
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
