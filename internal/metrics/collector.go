package metrics

import (
	"sync"

	"github.com/topcheer/ggcode/internal/safego"
)

// Collector receives MetricEvents on a buffered channel and persists them
// asynchronously via a callback. Events are dropped (never blocked) if
// the channel is full, so the agent is never slowed down.
type Collector struct {
	ch       chan MetricEvent
	stopCh   chan struct{}
	flushCh  chan chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// NewCollector starts a background goroutine that drains ch and calls persist
// for each event. cap is the channel buffer size (256 is a good default).
func NewCollector(capacity int, persist func(MetricEvent)) *Collector {
	c := &Collector{
		ch:      make(chan MetricEvent, capacity),
		stopCh:  make(chan struct{}),
		flushCh: make(chan chan struct{}),
		doneCh:  make(chan struct{}),
	}
	safego.Go("metrics.collector", func() {
		defer close(c.doneCh)
		for {
			select {
			case m := <-c.ch:
				persist(m)
			case ack := <-c.flushCh:
				for {
					select {
					case m := <-c.ch:
						persist(m)
					default:
						close(ack)
						goto next
					}
				}
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
		next:
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

// Flush blocks until all currently queued events have been persisted.
func (c *Collector) Flush() {
	if c == nil {
		return
	}
	ack := make(chan struct{})
	select {
	case <-c.doneCh:
		return
	case c.flushCh <- ack:
	}
	select {
	case <-ack:
	case <-c.doneCh:
	}
}

// Stop signals the collector goroutine to drain and exit.
func (c *Collector) Stop() {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}
