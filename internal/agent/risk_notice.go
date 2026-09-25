package agent

// risk_notice.go -- User-facing risk nudge surface.
//
// Research basis:
//   - Safety Nudges: User-Facing Interventions for Real-Time AI Risk
//     Awareness (arXiv:2609.26865, 2026): agent-safety interventions
//     work best when the USER, not only the model, perceives risk
//     events as they happen.
//   - CA-EUC (Curve Labs, 2026): calibrated abstention contracts are
//     only trustworthy when auditable by the operator.
//
// Problem: every risk intervention in the agent loop is model-facing.
// The irreversibility gate injects its advisory as a user-role context
// message (never rendered in the TUI chat list), and permission-policy
// denials surface only as collapsed tool error cards. In auto /
// autopilot modes - where approval prompts never fire - the operator
// has zero visibility into why the agent slowed down, re-planned, or
// got denied.
//
// Design:
//   - One-way best-effort channel: emitRiskNotice forwards a RiskNotice
//     to an optional handler (TUI today) and always leaves a debug.Log
//     trail so non-interactive runs stay auditable.
//   - LLM conversation untouched: notices never enter the message list,
//     so tool_calls/tool_results protocol pairing is unaffected.
//   - Bounded volume: the gate caps itself at 3 advisories per run and
//     policy denies are naturally rare; the handler must still be
//     non-blocking (same contract as SetToolProgressCallback).

import "github.com/topcheer/ggcode/internal/debug"

// RiskNotice is one user-facing risk event. Source names the producing
// intervention; Detail is a short human-readable reason.
type RiskNotice struct {
	Source string // e.g. "irreversibility-gate", "permission-policy"
	Tool   string // tool name involved ("" when not tool-scoped)
	Mode   string // permission mode at decision time ("" when N/A)
	Tier   int    // irreversibility tier 0-3 (irreversibility-gate only)
	Detail string // short human-readable reason
}

// TierName renders the irreversibility tier for display ("none" when
// unset; consumers that do not care about tiers simply ignore it).
func (n RiskNotice) TierName() string { return irrevTierName(n.Tier) }

// irrevTierName renders a gate tier for display.
func irrevTierName(tier int) string {
	switch tier {
	case irrevTierHigh:
		return "high"
	case irrevTierMedium:
		return "medium"
	case irrevTierLow:
		return "low"
	default:
		return "none"
	}
}

// SetRiskNoticeHandler installs the user-facing risk notification
// callback. May be nil (default): only the debug trail is written.
func (a *Agent) SetRiskNoticeHandler(fn func(RiskNotice)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRiskNotice = fn
}

// emitRiskNotice records a risk event and forwards it to the handler.
// Never blocks the tool loop on handler work.
func (a *Agent) emitRiskNotice(n RiskNotice) {
	debug.Log("agent", "risk-notice source=%s tool=%s mode=%s tier=%s: %s",
		n.Source, n.Tool, n.Mode, n.TierName(), n.Detail)
	a.mu.RLock()
	fn := a.onRiskNotice
	a.mu.RUnlock()
	if fn != nil {
		fn(n)
	}
}
