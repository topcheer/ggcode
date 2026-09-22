package agent

// Guidance Repeat Gate - run-scoped per-tag delivery cap with demotion.
//
// Research basis:
//   - arXiv:2607.08938 "Better Harnesses, Smaller Models: Building 90%
//     Cheaper Agents via Harness Engineering" (2026): harness quality - how
//     guidance is metered into context - dominates raw model capability for
//     routine tasks. Re-delivering an identical nudge every turn is pure
//     harness waste: it burns budget slots and context bytes without adding
//     information.
//   - Alert fatigue (Google SRE Book Ch.6, already the basis of
//     guidance_budget.go): a signal repeated every turn loses its marginal
//     value; the recipient stops reading it, so keeping the copies costs
//     attention and saves nothing.
//   - "Overthinking" / noise-reduction line of work (2026, e.g. the
//     Sketch-of-Thought family): trimming repeated meta-prompting cuts token
//     cost without hurting task accuracy.
//
// Problem: guidance_budget.go deduplicates per TURN, and individual detectors
// carry hand-tuned per-run quotas, but any tagged hint that keeps re-firing
// across turns (detectors without a quota, or with quotas looser than
// useful) can re-deliver indefinitely. Each repeat costs one of the two
// advisory budget slots (guidanceBudgetPerTurn = 2) that fresher signals
// need, and re-floods the context with text the model has already seen.
//
// Policy: a non-critical hint tag may be DELIVERED at most
// guidanceTagMaxDeliveries times per run (per-turn dedup already makes these
// distinct turns). After that the tag is demoted for the rest of the run:
//   - further hints with the tag are dropped at the gate BEFORE budget
//     accounting (they consume no slots, no bytes, no suppressed counters);
//   - the FIRST dropped hint is replaced by a one-line notice so the model
//     knows the earlier guidance still stands;
//   - later repeats are dropped silently (debug log only).
//
// Critical tags (criticalHintTags) and untagged hints always pass through:
// safety guidance must never be demoted.
//
// Protocol safety: everything stays inside the existing appendGuidance /
// injectGuidance channels (tool-result content / budget-checked user
// messages); no message is inserted between tool_calls and tool_results.
//
// Compaction: the ledger resets at compaction via guidanceCounterResets -
// the compacted-away text is invisible to the model, so "the earlier hint
// still stands" no longer holds and the cap must start over. This mirrors
// the B-class once-per-run quota contract in guidance_compact_reset.go.
// reset() (per-turn) deliberately does NOT touch this ledger.

import (
	"fmt"
	"strings"
	"sync"
)

const (
	// guidanceTagMaxDeliveries is how many times one non-critical hint tag
	// may be delivered per run before it is demoted. Three deliveries give
	// a detector a fair chance to be acted upon without letting a stuck
	// signal monopolize the per-turn advisory budget.
	guidanceTagMaxDeliveries = 3

	// guidancePausedTag is the tag on the one-time demotion notice. It is
	// advisory (not in criticalHintTags) so the notice itself is budgeted
	// and deduplicated like any other hint.
	guidancePausedTag = "guidance-paused"
)

// tagDemotion is the run-scoped delivery state of one hint tag.
type tagDemotion struct {
	deliveries   int
	demoted      bool
	noticeIssued bool
}

// guidanceDemoteLedger tracks per-tag delivery counts across the run.
// All methods are nil-safe so tests and the reset table can call them on a
// zero-value guidanceBudget.
type guidanceDemoteLedger struct {
	mu   sync.Mutex
	tags map[string]*tagDemotion
}

func newGuidanceDemoteLedger() *guidanceDemoteLedger {
	return &guidanceDemoteLedger{}
}

// reset clears the ledger. Called at compaction: the delivered text left the
// context, so the delivery cap restarts.
func (d *guidanceDemoteLedger) reset() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tags = nil
}

// noteDelivery records one delivered hint tag and demotes the tag once its
// delivery count exceeds guidanceTagMaxDeliveries.
func (d *guidanceDemoteLedger) noteDelivery(tag string) {
	if d == nil || tag == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.tags == nil {
		d.tags = make(map[string]*tagDemotion)
	}
	t := d.tags[tag]
	if t == nil {
		t = &tagDemotion{}
		d.tags[tag] = t
	}
	t.deliveries++
	if t.deliveries >= guidanceTagMaxDeliveries {
		t.demoted = true
	}
}

// demoted reports whether further hints with this tag are suppressed.
func (d *guidanceDemoteLedger) demoted(tag string) bool {
	if d == nil || tag == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.tags[tag]
	return t != nil && t.demoted
}

// initDemote lazily allocates the ledger. Agent-loop only (single
// goroutine), so a plain nil check suffices.
func (g *guidanceBudget) initDemote() {
	if g.demote == nil {
		g.demote = newGuidanceDemoteLedger()
	}
}

// filterDemoted applies the repeat gate to a hint before it reaches the
// per-turn budget:
//   - untagged / critical hints: returned unchanged;
//   - a tag below the delivery cap: returned unchanged (delivery is counted
//     later in allowDeduped, so budget-rejected hints do not consume cap);
//   - a demoted tag, first block: returns the one-time
//     [guidance-paused] notice text;
//   - a demoted tag, later blocks: returns "" (drop silently).
func (g *guidanceBudget) filterDemoted(hint string) string {
	tag := strings.ToLower(extractHintTag(hint))
	if tag == "" || isCriticalTag(tag) {
		return hint
	}
	g.initDemote()
	g.demote.mu.Lock()
	t := g.demote.tags[tag]
	if t == nil || !t.demoted {
		g.demote.mu.Unlock()
		return hint
	}
	first := !t.noticeIssued
	t.noticeIssued = true
	g.demote.mu.Unlock()
	if first {
		return fmt.Sprintf(
			"[%s] hint tag '%s' was already delivered %d times this run; further repeats are suppressed to save context - the earlier hint still stands.",
			guidancePausedTag, tag, guidanceTagMaxDeliveries)
	}
	return ""
}
