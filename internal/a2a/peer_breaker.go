package a2a

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const peerBreakerThreshold = 3

var peerBreakerCooldown = 60 * time.Second

type peerBreakerState uint8

const (
	peerBreakerClosed peerBreakerState = iota
	peerBreakerOpen
	peerBreakerHalfOpen
)

type peerBreaker struct {
	mu       sync.Mutex
	state    peerBreakerState
	failures int
	openedAt time.Time
}

func (b *peerBreaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case peerBreakerClosed:
		return true
	case peerBreakerOpen:
		if time.Since(b.openedAt) >= peerBreakerCooldown {
			b.state = peerBreakerHalfOpen
			return true
		}
		return false
	default:
		return false
	}
}

func (b *peerBreaker) record(infraFailed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case peerBreakerHalfOpen:
		if infraFailed {
			b.state = peerBreakerOpen
			b.openedAt = time.Now()
		} else {
			b.state = peerBreakerClosed
			b.failures = 0
		}
	case peerBreakerOpen:
		if !infraFailed {
			b.state = peerBreakerClosed
			b.failures = 0
		}
	default:
		if infraFailed {
			b.failures++
			if b.failures >= peerBreakerThreshold {
				b.state = peerBreakerOpen
				b.openedAt = time.Now()
			}
		} else {
			b.failures = 0
		}
	}
}

func (b *peerBreaker) stateSnapshot() peerBreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

var peerBreakers sync.Map

func peerBreakerFor(baseURL string) *peerBreaker {
	if baseURL == "" {
		return nil
	}
	if v, ok := peerBreakers.Load(baseURL); ok {
		return v.(*peerBreaker)
	}
	b := &peerBreaker{}
	actual, _ := peerBreakers.LoadOrStore(baseURL, b)
	return actual.(*peerBreaker)
}

func resetPeerBreakers() {
	peerBreakers.Range(func(k, _ any) bool {
		peerBreakers.Delete(k)
		return true
	})
}

type peerCircuitOpenError struct {
	Peer     string
	Method   string
	Cooldown time.Duration
}

func (e *peerCircuitOpenError) Error() string {
	return fmt.Sprintf(
		"a2a %s: peer circuit open for %s (recent infra failures); "+
			"retrying now wastes a full timeout round-trip - wait %v or pick another peer",
		e.Method, e.Peer, e.Cooldown)
}

func classifyRPCError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var rpcErr *JSONRPCError
	if errors.As(err, &rpcErr) {
		return false
	}
	msg := err.Error()
	for _, local := range []string{
		"marshal params",
		"marshal request",
		": decode:",
		"re-marshal result",
		"unmarshal result",
	} {
		if strings.Contains(msg, local) {
			return false
		}
	}
	return true
}
