package metrics

import (
	"github.com/topcheer/ggcode/internal/safego"
)

// Collector receives MetricEvents on a buffered channel and persists them
// asynchronously via a callback. Events are dropped (never blocked) if
// the channel is full, so the agent is never slowed down.
type Collector struct {
	ch     chan MetricEvent
	stopCh chan struct{}
}

// NewCollector starts a background goroutine that drains ch and calls persist
// for each event. cap is the channel buffer size (256 is a good default).
func NewCollector(capacity int, persist func(MetricEvent)) *Collector {
	c := &Collector{
		ch:     make(chan MetricEvent, capacity),
		stopCh: make(chan struct{}),
	}
	safego.Go("metrics.collector", func() {
		for {
			select {
			case m := <-c.ch:
				persist(m)
			case <-c.stopCh:
				// Drain remaining events
				for {
					select {
					case m := <-c.ch:
						persist(m)
					default:
						return
					}
				}
			}
		}
	})
	return c
}

// Emit sends a metric event to the collector. Non-blocking: drops if full.
func (c *Collector) Emit(m MetricEvent) {
	select {
	case c.ch <- m:
	default:
		// drop — don't block the agent
	}
}

// Stop signals the collector goroutine to drain and exit.
func (c *Collector) Stop() {
	close(c.stopCh)
}
