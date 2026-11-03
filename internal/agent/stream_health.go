package agent

import (
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

const streamStallThreshold = 30 * time.Second

func streamWithStallDetection(stream <-chan provider.StreamEvent, stallThreshold time.Duration) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	safego.Go("stream.stall_detection", func() {
		defer close(out)
		timer := time.NewTimer(stallThreshold)
		defer timer.Stop()
		warned := false
		for {
			select {
			case event, ok := <-stream:
				if !ok {
					return
				}
				warned = false

				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				out <- event
				timer.Reset(stallThreshold)
			case <-timer.C:
				if !warned {
					warned = true
					out <- provider.StreamEvent{
						Type: provider.StreamEventSystem,
						Text: fmt.Sprintf("stream stall: no data for %s, connection may be degraded", stallThreshold),
					}
				}
				timer.Reset(stallThreshold)
			}
		}
	})
	return out
}
