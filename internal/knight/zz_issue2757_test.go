package knight

// Issue #2757 probe: Status() and CanPerformTask() read k.cfg.Enabled /
// k.running / k.lock without holding k.mu while Start/Stop/Enable write
// them under the lock (Running() is the correct pattern). A -race build on
// concurrent Status-vs-Stop flags the race; after the fix both readers
// snapshot under k.mu.

import (
	"github.com/topcheer/ggcode/internal/config"
	"sync"
	"testing"
	"time"
)

func TestIssue2757StatusConcurrentWithStopNoRace(t *testing.T) {
	k := &Knight{cfg: config.KnightConfig{Enabled: true}, running: true, budget: &Budget{}}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Readers hammer Status/CanPerformTask while the writer flips running
	// under k.mu - with the unlocked readers, -race flags the data race;
	// after the fix (snapshot under k.mu) the same loop is clean.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = k.Status()
					_ = k.CanPerformTask()
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				k.mu.Lock()
				k.running = !k.running
				k.mu.Unlock()
			}
		}
	}()
	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()
}
