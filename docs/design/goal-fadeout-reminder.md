# Goal Fade-Out Reminder (event-driven system reminders)

- Round: r94
- Research basis: OpenDev, "Building Effective AI Coding Agents for the Terminal" (arXiv:2603.05344) — instruction compliance degrades over long conversations ("instruction fade-out") even though the instructions remain in context; cadence-based reminder re-injection recovered compliance (first violation: turn 7 → turn 19 in the reference experiment). Secondary: Cobus Greyling's replication ("Instruction Fade-Out Is the Silent Killer of AI Agents").
- Implementation: `internal/agent/goal_reminder.go`, wired in the agent run loop next to the other iteration-level guidance emitters, routed through `injectGuidance` (#677 per-turn budget, #1206 byte pool, #681 delivered-vs-returned discipline).

## Problem

The active autopilot goal is injected into the system prompt exactly once at `Run()` start (`maybeInjectDynamicSystemPrompt`, layer 1.5). In autopilot runs of 50-200 iterations that standing instruction fades from effective attention — nothing re-anchors it mid-run. `constraint_amnesia.go` covers only regex-extracted USER constraints (one warning per run).

## Design

- Pure turn cadence, open loop: NOT a detector — no behavioral pattern is detected; the reminder fires on schedule when a goal is active.
- Gates: no reminder before iteration 12; ≥15 iterations between reminders; ≤5 per run (~60 tokens each, bounded context cost).
- Disabled entirely when no autopilot goal is set (interactive runs untouched).
- Budget-suppressed reminders refund their cadence slot (`markUndelivered`) and retry next iteration.
- Mid-run compaction refunds the quota (`resetGuidanceCounters`): the goal is re-emitted in the system prompt after compaction, restarting the fade clock.

## Boundary with adjacent mechanisms

| Mechanism | Content source | Timing | Surface |
|---|---|---|---|
| r80 ratchet rules | learned rules store (selection/dedup/staleness) | once per task start | system prompt |
| constraint amnesia | regex-extracted user constraints | once per run, ≥12 iters | guidance message |
| goal reminder (this) | active autopilot goal | every 15 iters, ≤5/run | guidance message |

## Tests

`internal/agent/goal_reminder_test.go`: disabled-without-goal, first-iteration gate, cadence window + boundary, per-run cap, suppressed-retry refund, wrong-slot refund isolation, reset, nil-receiver safety. Compaction registry remains pinned by `Test1826QuotaRegistryPin` (one-directional: new resetter needs no registration).
