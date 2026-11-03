package knight

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
