package metrics

import (
	"context"
	"sync"

	"github.com/topcheer/ggcode/internal/safego"
)

type Collector struct {
	ch       chan MetricEvent
	stopCh   chan struct{}
	flushCh  chan chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

func NewCollector(ctx context.Context, capacity int, persist func(MetricEvent)) *Collector {
	if ctx == nil {
		ctx = context.Background()
	}
	c := &Collector{
		ch:      make(chan MetricEvent, capacity),
		stopCh:  make(chan struct{}),
		flushCh: make(chan chan struct{}),
		doneCh:  make(chan struct{}),
	}
	safego.Go("metrics.collector", func() {
		defer close(c.doneCh)
		drain := func() {
			for {
				select {
				case m := <-c.ch:
					persist(m)
				default:
					return
				}
			}
		}
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
				drain()
				return
			case <-ctx.Done():
				drain()
				return
			}
		next:
		}
	})
	return c
}

func (c *Collector) Emit(m MetricEvent) {
	select {
	case c.ch <- m:
	default:

	}
}

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

func (c *Collector) Stop() {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	<-c.doneCh
}
