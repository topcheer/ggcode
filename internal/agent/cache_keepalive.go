package agent

import (
	"context"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// cacheKeepaliveInterval is how often we ping the provider to keep the
	// prompt cache warm. Anthropic's cache TTL is 300s (5 min, sliding),
	// so we use 270s (TTL - 30s margin) to stay safely within the window.
	cacheKeepaliveInterval = 270 * time.Second

	// cacheKeepaliveMaxTouches caps the total number of keepalive pings
	// to prevent indefinite background API calls. 12 touches × 270s ≈ 54 min.
	cacheKeepaliveMaxTouches = 12
)

// cacheKeepaliveState manages background prompt-cache warming pings.
//
// Research: Anthropic prompt caching has a 300s sliding TTL. When an agent
// finishes a run and the user goes idle, the cache expires after 5 minutes.
// The next user message then pays full price to re-cache the prefix (system
// prompt + conversation history), which can be 50-100K+ tokens.
//
// By sending a minimal ping request every 270s during idle, we keep the cache
// warm. Each touch costs ~0.1× the prefix token cost. Breakeven at 17.4%
// resume probability. Savings: ~83K tokens per 9-min idle period.
//
// Only enabled for providers that support prompt caching (currently Anthropic).
type cacheKeepaliveState struct {
	mu       sync.Mutex
	timer    *time.Timer
	touches  int
	cancelFn context.CancelFunc
	provider provider.Provider
	messages []provider.Message
	tools    []provider.ToolDefinition
}

func newCacheKeepaliveState() *cacheKeepaliveState {
	return &cacheKeepaliveState{}
}

// startIdle begins sending periodic keepalive pings to the provider.
// It captures the current conversation messages and tool definitions so
// the ping request matches the cached prefix. Called after an agent run ends.
//
// The ping sends a minimal user message ("ok") with the full conversation
// history and tool list. The provider sees this as a continuation, hitting
// the cached prefix, and resets the cache TTL.
func (k *cacheKeepaliveState) startIdle(p provider.Provider, messages []provider.Message, tools []provider.ToolDefinition) {
	k.stopIdle()

	// Only enable for Anthropic (prompt caching provider)
	if p == nil || p.Name() != "anthropic" {
		return
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	k.provider = p
	k.messages = messages
	k.tools = tools
	k.touches = 0

	ctx, cancel := context.WithCancel(context.Background())
	k.cancelFn = cancel

	k.scheduleNext(ctx)
}

// stopIdle cancels any pending keepalive timer and resets state.
// Called when a new user message arrives (new run starts) or on Close().
func (k *cacheKeepaliveState) stopIdle() {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.timer != nil {
		k.timer.Stop()
		k.timer = nil
	}
	if k.cancelFn != nil {
		k.cancelFn()
		k.cancelFn = nil
	}
	k.touches = 0
	k.provider = nil
	k.messages = nil
	k.tools = nil
}

// scheduleNext arms the timer for the next keepalive ping.
// Must be called with k.mu held.
func (k *cacheKeepaliveState) scheduleNext(ctx context.Context) {
	if k.touches >= cacheKeepaliveMaxTouches {
		debug.Log("agent", "cache keepalive: reached max touches (%d), stopping", k.touches)
		return
	}

	k.timer = time.AfterFunc(cacheKeepaliveInterval, func() {
		k.doPing(ctx)
	})
}

// doPing sends a minimal request to the provider to keep the cache warm.
func (k *cacheKeepaliveState) doPing(ctx context.Context) {
	k.mu.Lock()
	p := k.provider
	msgs := k.messages
	tools := k.tools
	k.touches++
	touchNum := k.touches
	k.mu.Unlock()

	if p == nil || ctx.Err() != nil {
		return
	}

	// Send a minimal ping: just "ok" as user content, with full history.
	// The provider will process this against the cached prefix, refreshing TTL.
	pingMsgs := make([]provider.Message, 0, len(msgs)+1)
	pingMsgs = append(pingMsgs, msgs...)
	pingMsgs = append(pingMsgs, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "ok"},
		},
	})

	debug.Log("agent", "cache keepalive: sending ping #%d (prefix=%d messages)", touchNum, len(msgs))

	// Use a short timeout — this is a background ping, not user-facing.
	pingCtx, pingCancel := context.WithTimeout(ctx, 30*time.Second)
	defer pingCancel()

	// Use non-streaming Chat (simpler, we don't need the response content).
	// We use the tools list to match the cached prefix shape exactly.
	_, err := p.Chat(pingCtx, pingMsgs, tools)
	if err != nil {
		debug.Log("agent", "cache keepalive: ping #%d failed: %v", touchNum, err)
	} else {
		debug.Log("agent", "cache keepalive: ping #%d succeeded", touchNum)
	}

	// Schedule the next ping if we haven't hit the cap.
	k.mu.Lock()
	if k.cancelFn != nil && ctx.Err() == nil && k.touches < cacheKeepaliveMaxTouches {
		k.scheduleNext(ctx)
	}
	k.mu.Unlock()
}

// isActive returns true if keepalive is currently running.
func (k *cacheKeepaliveState) isActive() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.timer != nil
}

// touchCount returns the number of keepalive pings sent so far.
func (k *cacheKeepaliveState) touchCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.touches
}
