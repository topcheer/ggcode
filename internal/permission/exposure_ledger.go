package permission

// Composition-exposure ledger: session-scoped state that remembers when a
// file read placed secret-bearing or out-of-sandbox content into model
// context, so that a LATER egress call can be gated even when its arguments
// carry no recognizable secret signature.
//
// Motivation (r255 research): ToolGuardian (arXiv:2607.21835) and Microsoft's
// Agent Governance Toolkit (2026-04) both identify COMPOSITIONAL RISK — tool
// calls that are individually benign (an approved read, an approved web
// fetch) but form an exfiltration channel in sequence — as the runtime
// enforcement gap that per-call signature checks cannot close. All existing
// gates in this package (IsNetworkExfiltrate, hasDataEgressSignature,
// sensitive-path read gates) judge a SINGLE call in isolation; none of them
// can see that the payload was read three calls ago. The classic hole: a
// supervised-mode read of an out-of-sandbox, non-sensitive path was a silent
// Allow, and the later web_fetch of that content (read-only tool) was also a
// silent Allow — zero prompts for a complete read→exfiltrate loop.
//
// This is deliberately stateful POLICY, not another static detector: the
// sensitive-path characterization reuses isSensitivePath, recording is
// decision-aware (only content that actually reached model context is
// recorded), and entries age out after a call-count window or wall-clock
// TTL, whichever expires first, keeping the false-positive surface bounded.

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// exposureCallWindow is how many tool calls a recorded exposure stays
	// live. Long enough to span a typical read→transform→egress chain,
	// short enough that a legitimate much-later egress is not nagged by a
	// stale window.
	exposureCallWindow = 50

	// exposureTTL bounds an exposure by wall-clock time regardless of how
	// few calls happen in between: an idle session must not keep a stale
	// window open indefinitely.
	exposureTTL = 15 * time.Minute
)

// exposureEvent is one recorded read that exposed content to the model.
type exposureEvent struct {
	path  string
	at    time.Time
	stamp int // tool-call counter value at record time
}

// exposureLedger tracks recent secret-exposure reads for one session.
type exposureLedger struct {
	mu     sync.Mutex
	events []exposureEvent
	calls  int
}

func newExposureLedger() *exposureLedger {
	return &exposureLedger{}
}

// onToolCall advances the call counter and prunes expired events. Called
// once per Check so aging reflects real agent activity, not wall time alone.
func (l *exposureLedger) onToolCall() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.pruneLocked(time.Now())
}

// record notes that a read of path exposed its content to the model.
func (l *exposureLedger) record(path string) {
	if path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, exposureEvent{
		path:  filepath.ToSlash(filepath.Clean(path)),
		at:    time.Now(),
		stamp: l.calls,
	})
}

// active reports whether any exposure event is still live and returns the
// most recent path that caused it (for debug/status surfacing).
func (l *exposureLedger) active() (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(time.Now())
	if len(l.events) == 0 {
		return false, ""
	}
	return true, l.events[len(l.events)-1].path
}

func (l *exposureLedger) prune(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
}

func (l *exposureLedger) pruneLocked(now time.Time) {
	cutoff := now.Add(-exposureTTL)
	kept := l.events[:0]
	for _, e := range l.events {
		if e.at.Before(cutoff) || l.calls-e.stamp > exposureCallWindow {
			debug.Log("permission", "exposure ledger: entry expired (path=%s)", e.path)
			continue
		}
		kept = append(kept, e)
	}
	l.events = kept
}
