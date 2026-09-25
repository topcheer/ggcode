package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// DenyLedger records pre_tool_use hook denials for the lifetime of a session.
//
// Background (arXiv 2609.30217, EvasionBench): hook denials surface as
// transient tool_result entries. Under ordinary task pressure, agents retry
// blocked operations with rephrased, encoded, or split-out variants and
// keep retrying until the denial scrolls out of the monitor's visible
// history — after compaction the policy decision is gone entirely, so the
// agent "forgets" the constraint and the hook author gets no signal that
// evasion was attempted.
//
// The ledger gives denial decisions three sticky properties:
//
//  1. every new denial restates the running attempt count for that tool,
//     so re-armed attempts always re-enter the visible history with the
//     prior blocks attached;
//  2. a compact per-tool denial summary is re-injected as a durable
//     system note after each compaction (via AddPostCompactNoteProvider),
//     so the policy survives history loss;
//  3. the ledger itself is harness-owned agent state, outside the
//     model's write reach.
//
// All methods are nil-receiver safe so Agent struct literals in tests
// (which do not run newDenyLedger) keep working unchanged.
type DenyLedger struct {
	mu     sync.Mutex
	events []hookDenyEvent
}

const (
	// denyLedgerMaxEvents bounds memory; denials are rare and the compact
	// summary aggregates counts, so a bounded ring is sufficient.
	denyLedgerMaxEvents = 64
	// denyReasonSnippetLimit truncates hook output echoed in the durable
	// note; hook reasons can embed large payloads (issue #684 payloads).
	denyReasonSnippetLimit = 160
)

type hookDenyEvent struct {
	Tool   string
	Reason string
	At     time.Time
}

func newDenyLedger() *DenyLedger {
	return &DenyLedger{}
}

// Record appends a denial for toolName. reason is the hook's stdout/explanation
// (already truncated for hook env payloads, but clamped again here because the
// reason is re-echoed in the durable compaction note).
func (l *DenyLedger) Record(toolName, reason string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) >= denyLedgerMaxEvents {
		l.events = l.events[1:]
	}
	l.events = append(l.events, hookDenyEvent{
		Tool:   toolName,
		Reason: clampSnippet(reason, denyReasonSnippetLimit),
		At:     time.Now(),
	})
}

// StickyFeedback renders the per-attempt denial suffix appended to the
// hook's own denial output. It includes the attempt number for this tool
// (the current denial included) and the session-wide block count, and
// states explicitly that the policy survives compaction. Returns "" when
// there is nothing on record (defensive; callers Record first).
func (l *DenyLedger) StickyFeedback(toolName string) string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	forTool := 0
	for _, ev := range l.events {
		if ev.Tool == toolName {
			forTool++
		}
	}
	if forTool == 0 {
		return ""
	}
	return fmt.Sprintf(
		"\n[hook policy enforcement] blocked attempt #%d for tool %q (%d hook block(s) recorded this session). "+
			"This policy decision remains binding after context compaction. "+
			"Do not rephrase, encode, split across tools, or otherwise retry variants of the blocked operation; "+
			"change approach or ask the user to adjust hooks.",
		forTool, toolName, len(l.events))
}

// PostCompactNote renders the durable hook-policy summary re-injected after
// each compaction. Returns "" while no denials are on record, which keeps
// the post-compaction note absent for clean sessions.
func (l *DenyLedger) PostCompactNote() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) == 0 {
		return ""
	}
	counts := make(map[string]int)
	var order []string
	for _, ev := range l.events {
		if counts[ev.Tool] == 0 {
			order = append(order, ev.Tool)
		}
		counts[ev.Tool]++
	}
	var b strings.Builder
	b.WriteString("Hook policy state: pre_tool_use hook denials recorded this session: ")
	for i, t := range order {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s x%d", t, counts[t])
	}
	last := l.events[len(l.events)-1]
	fmt.Fprintf(&b, ". Last denial (tool %q): %s. These denials remain binding after compaction; do not retry blocked operations or variants of them.",
		last.Tool, last.Reason)
	return b.String()
}

func clampSnippet(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
