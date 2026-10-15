# Stale-Observation Re-Verify Escalation

## Problem

AI coding agents build conclusions on negative observations — "grep returned
no matches", "file not found", "search came back empty" — and then keep
reasoning from them long after mutations have changed the underlying state.
The run's epistemic detectors are deep on the detection side (false premise,
cross-turn contradiction, belief-defense escalation), but every recovery path
is a one-shot lexical nudge. The agent gets reminded that its belief may be
wrong, but is never forced to refresh the observation itself.

Research basis: PoS — explicit belief states with belief-trapping detection
and *recovery* (arXiv:2610.01415). PoS's differentiated finding is that
detecting a stale belief is necessary but not sufficient: recovery means
re-executing the stale observation (re-validating the world), not stacking
more reminders. Found by research round sa-134 (2026-10), which also
confirmed the `agent.go` tool-result recording comment promised negative-
observation recording "for later delayed contradiction analysis" with no
backing implementation — this detector is that implementation.

No major competitor (Claude Code, Cursor, OpenHands, Devin, Aider) has a
re-verification recovery channel; guidance nudges are the industry-standard
ceiling.

## Solution

A deterministic, zero-LLM-cost detector (`internal/agent/reverify_escalation.go`)
that upgrades recovery from "note this" to "re-run and cite the new output".

### How it works

1. **Recording**: every *successful* tool result containing no-match /
   not-found / empty indicators is stored as a negative observation
   (tool name, arg summary, snippet, iteration). Mutation tool calls
   advance a mutation-recency marker.
2. **Supersede**: a later successful, non-negative result from the same tool
   drops that tool's unconsumed observations (same semantics as the false
   premise detector's fail-then-fix supersede, #331).
3. **Detection**: when the assistant makes a positive/success claim that
   does not acknowledge any error, and an observation is aged (>= 6
   iterations) with mutations having happened after it, the observation is
   consumed and a hard instruction is injected: re-run that observation and
   cite the new output before further edits or completion claims.
4. **Uncertainty**: each escalation feeds the compounded-uncertainty
   accumulator (1.5 units — between hedging and false premise).

The tool is never auto-executed (permission/side-effect safety); recovery is
an instruction upgrade, not an action.

### Thresholds

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| `reverifyStaleAgeIters` | 6 iterations | Younger contradictions are the false-premise detector's freshness-window domain; this channel targets delayed traps |
| `reverifyMaxWarnings` | 2 per run | Noise cap — same budget family as the other epistemic detectors (avoids the 387282a6 noise trade-off) |
| `reverifyMaxObservations` | 20 | Bounded memory |
| `weightStaleObs` | 1.5 | Riskier than hedging, less than a proven false premise |

### Distinct from

- `false_premise_check.go`: reacts to tool *errors* within a 2-turn
  freshness window; this reacts to aged negatives from *successful* calls.
- `redundant_reverify.go`: discourages repeated re-verification; this
  *demands* re-verification when a mutation may have invalidated a negative
  observation still being relied upon.
- temporal-blindness family: watches verification claims about mutated
  code; this watches negative observations whose staleness is *caused by*
  later mutations.
