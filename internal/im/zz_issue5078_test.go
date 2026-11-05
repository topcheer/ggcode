package im

import (
	"context"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #5078: Start assigned a.server AFTER autoBind with no synchronization, so a
// concurrent Send raced on the unsynchronized write (nil-tolerant read window
// + data race). The fix assigns before registration AND guards a.server with
// the adapter mutex. This harness runs Start concurrently with Send hammers
// under -race: any unsynchronized a.server access fails the race detector.
func TestIssue5078StartSendRace(t *testing.T) {
	mgr := NewManager()
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}

	for i := 0; i < 3; i++ {
		a := newDummyAdapter("race-t", config.IMConfig{}, config.IMAdapterConfig{}, mgr)
		if a.server != nil {
			t.Fatal("server must start nil")
		}

		done := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for {
				select {
				case <-done:
					return
				default:
					_ = a.Send(ctx, ChannelBinding{}, OutboundEvent{})
				}
			}
		}()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		a.Start(ctx) // assigns server (mu-guarded), then autoBind, returns promptly
		close(done)
		wg.Wait()

		a.mu.Lock()
		got := a.server
		a.mu.Unlock()
		if got == nil {
			t.Fatal("server must be assigned after Start ran")
		}
	}
}
