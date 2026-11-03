package swarm

import (
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	teammateBreakerThreshold = 3
)

var teammateBreakerCooldown = 60 * time.Second

type teammateBreakerState int

const (
	teammateBreakerClosed teammateBreakerState = iota
	teammateBreakerOpen
	teammateBreakerHalfOpen
)

type teammateBreaker struct {
	mu       sync.Mutex
	state    teammateBreakerState
	failures int
	openedAt time.Time
}

func (b *teammateBreaker) tripped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case teammateBreakerClosed:
		return false
	case teammateBreakerOpen:
		return time.Since(b.openedAt) < teammateBreakerCooldown
	default:

		if time.Since(b.openedAt) >= 2*teammateBreakerCooldown {
			b.state = teammateBreakerOpen
			b.openedAt = time.Now()
		}
		return true
	}
}

func (b *teammateBreaker) consumeProbe() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != teammateBreakerOpen {
		return false
	}
	if time.Since(b.openedAt) < teammateBreakerCooldown {
		return false
	}
	b.state = teammateBreakerHalfOpen
	b.openedAt = time.Now()
	return true
}

func (b *teammateBreaker) record(failed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case teammateBreakerHalfOpen:
		if failed {
			b.state = teammateBreakerOpen
			b.openedAt = time.Now()
		} else {
			b.state = teammateBreakerClosed
			b.failures = 0
		}
	case teammateBreakerOpen:

		if !failed {
			b.state = teammateBreakerClosed
			b.failures = 0
		}
	default:
		if failed {
			b.failures++
			if b.failures >= teammateBreakerThreshold {
				b.state = teammateBreakerOpen
				b.openedAt = time.Now()
			}
		} else {
			b.failures = 0
		}
	}
}

func (b *teammateBreaker) stateSnapshot() teammateBreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

var teammateBreakers sync.Map

func teammateBreakerFor(id string) *teammateBreaker {
	if id == "" {
		return nil
	}
	if v, ok := teammateBreakers.Load(id); ok {
		return v.(*teammateBreaker)
	}
	b := &teammateBreaker{}
	actual, _ := teammateBreakers.LoadOrStore(id, b)
	return actual.(*teammateBreaker)
}

func teammateClaimAllowed(teammateID string) bool {
	b := teammateBreakerFor(teammateID)
	if b == nil {
		return true
	}
	allowed := !b.tripped()
	if !allowed {
		debug.Log("swarm", "[teammate-breaker] %s barred from claiming (circuit open)", teammateID)
	}
	return allowed
}

func teammateConsumeProbe(teammateID string) {
	b := teammateBreakerFor(teammateID)
	if b == nil {
		return
	}
	if b.consumeProbe() {
		debug.Log("swarm", "[teammate-breaker] %s probe task claimed (half-open)", teammateID)
	}
}

func recordTeammateTaskResult(teammateID string, failed bool) {
	b := teammateBreakerFor(teammateID)
	if b == nil {
		return
	}
	b.record(failed)
	if failed && b.stateSnapshot() == teammateBreakerOpen {
		debug.Log("swarm", "[teammate-breaker] %s circuit OPEN after consecutive failures", teammateID)
	}
}

func resetTeammateBreakers() {
	teammateBreakers.Range(func(k, _ any) bool {
		teammateBreakers.Delete(k)
		return true
	})
}
