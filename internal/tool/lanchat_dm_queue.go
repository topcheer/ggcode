package tool

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// #5558 residual 1 + 2: sender-local opt-in DM retry queue with backoff and
// dual-dimension frequency budgets (per-pair cooldown + per-sender window).
// Fail-safe by design: queue overflow and TTL expiry surface explicit errors,
// never silent drops; the queue is OFF unless LANCHAT_DM_RETRY_QUEUE=1.

const (
	defaultSenderDMWindow    = time.Minute
	defaultSenderDMMax       = 8
	dmQueueOverallCap        = 8
	dmQueueTTL               = 10 * time.Minute
	dmQueueFlushInterval     = 5 * time.Second
	dmQueueMaxFlush_attempts = 2
)

// senderDMBudget tracks how many DMs one sender issued across ALL recipients
// inside a rolling window (#5558 Residual 2).
type senderDMBudget struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	stamps map[string][]time.Time // sender -> send timestamps
}

func newSenderDMBudget() *senderDMBudget {
	return &senderDMBudget{window: defaultSenderDMWindow, max: defaultSenderDMMax, stamps: make(map[string][]time.Time)}
}

// check returns remaining cooldown until the sender may send again, 0 if allowed.
func (b *senderDMBudget) check(sender string) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	st := b.prune(sender, now)
	if len(st) < b.max {
		return 0
	}
	// earliest stamp + window = when a slot frees
	return st[0].Add(b.window).Sub(now)
}

func (b *senderDMBudget) record(sender string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.stamps[sender] = append(b.prune(sender, now), now)
}

func (b *senderDMBudget) prune(sender string, now time.Time) []time.Time {
	st := b.stamps[sender]
	keep := st[:0]
	for _, ts := range st {
		if now.Sub(ts) < b.window {
			keep = append(keep, ts)
		}
	}
	b.stamps[sender] = keep
	return keep
}

// dmBlock names which budget dimension tripped and its independent countdown.
type dmBlock struct {
	dimension string // "pair" | "sender-budget"
	remaining time.Duration
}

func (d dmBlock) message() string {
	if d.dimension == "sender-budget" {
		return fmt.Sprintf(
			"rate-limited (sender budget %d/%s used, %s remaining) - dimension=sender-budget - use sleep tool with seconds=%d to defer retry",
			defaultSenderDMMax, formatWindow(defaultSenderDMWindow), formatCooldown(d.remaining), int(d.remaining.Seconds())+1)
	}
	return fmt.Sprintf(
		"rate-limited (%s remaining) - use sleep tool with seconds=%d to defer retry",
		formatCooldown(d.remaining), int(d.remaining.Seconds())+1)
}

// dmRetryQueue is a bounded sender-local holding area for agent DMs that hit
// the rate limiter (#5558 Residual 1). At most one pending message per
// (sender, recipient) pair, dmQueueOverallCap overall, dmQueueTTL expiry.
type dmRetryQueue struct {
	mu      sync.Mutex
	pending map[string]dmQueueEntry // "sender→recipient" -> entry
	// senderKey identifies the local seat for cap accounting.
}

type dmQueueEntry struct {
	text        string
	asAgent     bool
	queuedAt    time.Time
	cooldownEnd time.Time // when the per-pair cooldown expires
	attempts    int
}

func newDMRetryQueue() *dmRetryQueue {
	return &dmRetryQueue{pending: make(map[string]dmQueueEntry)}
}

type dmQueueResult struct {
	Queued   bool
	Replaced bool // displaced an older pending message for the same pair
	Overflow bool
	WaitHint time.Duration
}

// offer enqueues a blocked DM. Fail-safe: overflow returns Overflow=true and
// the caller must surface an error (never drop silently).
func (q *dmRetryQueue) offer(sender, recipient string, text string, asAgent bool, cooldownEnd time.Time) dmQueueResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	key := dmPairKey(sender, recipient)
	_, exists := q.pending[key]
	if !exists && len(q.pending) >= dmQueueOverallCap {
		return dmQueueResult{Overflow: true}
	}
	q.pending[key] = dmQueueEntry{text: text, asAgent: asAgent, queuedAt: time.Now(), cooldownEnd: cooldownEnd}
	return dmQueueResult{Queued: true, Replaced: exists, WaitHint: time.Until(cooldownEnd)}
}

// takeDue pops one entry whose backoff has expired (cooldown reached or TTL
// forced). Entries past TTL are expired, not retried - surfaced as errors.
func (q *dmRetryQueue) takeDue(sender string, now time.Time) (key string, e dmQueueEntry, ok bool, expired bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for k, v := range q.pending {
		parts := strings.SplitN(k, "\u2192", 2)
		if len(parts) != 2 || parts[0] != sender {
			continue
		}
		if now.Sub(v.queuedAt) > dmQueueTTL {
			delete(q.pending, k)
			return k, v, false, true
		}
		if now.After(v.cooldownEnd) {
			delete(q.pending, k)
			return k, v, true, false
		}
	}
	return "", dmQueueEntry{}, false, false
}

func (q *dmRetryQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// peekFlushIn reports how long until the given pair's pending entry may flush.
func (q *dmRetryQueue) peekFlushIn(sender, recipient string, now time.Time) time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.pending[dmPairKey(sender, recipient)]
	if !ok {
		return 0
	}
	d := e.cooldownEnd.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

func dmPairKey(sender, recipient string) string { return sender + "\u2192" + recipient }

func dmQueueEnabled() bool { return os.Getenv("LANCHAT_DM_RETRY_QUEUE") == "1" }

// flushPending drains due entries for the local seat via the provided sender.
// Returns human-readable flush report lines (success and failures).
func (t *LanChatTool) flushPending(ctx context.Context, selfID string) []string {
	if t.dmQueue == nil {
		return nil
	}
	var lines []string
	for {
		key, e, ok, expired := t.dmQueue.takeDue(selfID, time.Now())
		if key == "" {
			break
		}
		recipient := strings.SplitN(key, "\u2192", 2)[1]
		if expired {
			lines = append(lines, fmt.Sprintf("DM retry queue: TTL expired for %s, message dropped: %.60s", t.displayName(recipient), e.text))
			continue
		}
		if !ok {
			continue
		}
		// respect the dual-dimension budget before flushing
		if wait := t.rateLimiter.senderBudget.check(selfID); wait > 0 {
			// re-queue behind the sender budget with updated cooldown end
			t.dmQueue.mu.Lock()
			t.dmQueue.pending[key] = e
			t.dmQueue.pending[key] = dmQueueEntry{text: e.text, asAgent: e.asAgent, queuedAt: e.queuedAt, cooldownEnd: time.Now().Add(wait), attempts: e.attempts}
			t.dmQueue.mu.Unlock()
			break // all further entries face the same budget; stop this round
		}
		var err error
		if e.asAgent {
			err = t.Hub.SendAsAgent(ctx, recipient, lanchat.RoleAgent, e.text)
		} else {
			err = t.Hub.SendDirect(ctx, recipient, lanchat.RoleHuman, e.text, nil)
		}
		if err != nil {
			if e.attempts+1 >= dmQueueMaxFlush_attempts {
				lines = append(lines, fmt.Sprintf("DM retry queue: giving up on %s after %d attempts: %v", t.displayName(recipient), e.attempts+1, err))
				continue
			}
			t.dmQueue.mu.Lock()
			t.dmQueue.pending[key] = dmQueueEntry{text: e.text, asAgent: e.asAgent, queuedAt: time.Now(), cooldownEnd: time.Now().Add(dmQueueFlushInterval), attempts: e.attempts + 1}
			t.dmQueue.mu.Unlock()
			continue
		}
		t.rateLimiter.recordDM(selfID, recipient)
		t.rateLimiter.senderBudget.record(selfID)
		lines = append(lines, fmt.Sprintf("DM retry queue: flushed to %s", t.displayName(recipient)))
	}
	return lines
}

func formatWindow(d time.Duration) string { return formatCooldown(d) }

// tryQueueBlockedDM offers a rate-blocked DM to the opt-in queue and returns a
// human-readable line for the send result. Queue disabled or overflow -> not
// queued (caller falls back to the plain dimension message / error).
func (t *LanChatTool) tryQueueBlockedDM(selfID, recipient, content string, asAgent bool, b dmBlock) (bool, string) {
	if t.dmQueue == nil {
		return false, ""
	}
	cooldownEnd := time.Now().Add(b.remaining)
	res := t.dmQueue.offer(selfID, recipient, content, asAgent, cooldownEnd)
	if res.Overflow {
		return false, fmt.Sprintf("queue full (%d/%d) - NOT queued, message rejected", dmQueueOverallCap, dmQueueOverallCap)
	}
	verb := "queued"
	if res.Replaced {
		verb = "replaced prior pending message and queued"
	}
	return true, fmt.Sprintf(
		"%s for auto-flush in %s (dimension=%s) - no action needed",
		verb, formatCooldown(b.remaining), b.dimension)
}

// dmQueueFlushLoop drives automatic backoff flushes while the tool lives.
func (t *LanChatTool) dmQueueFlushLoop(ctx context.Context) {
	ticker := time.NewTicker(dmQueueFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if t.Hub == nil {
				continue
			}
			t.flushPending(ctx, t.Hub.NodeID())
		}
	}
}
