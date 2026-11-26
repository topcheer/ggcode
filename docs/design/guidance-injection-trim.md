# Guidance Injection Trim

> Status: implemented in #5940 and its follow-up (2026-11-24). This is the
> record of *what was removed, what deliberately stayed, and why*.

## What "guidance" was

`internal/agent` grew ~150 "detectors" - little state machines that watch the
tool loop and the conversation and, when some pattern tripped, injected a
bracketed reminder into the model's context. Two funnels carried them:

| Funnel | Where it lands | Examples |
|---|---|---|
| `appendGuidance(&result, hint)` | appended to a tool result | `[Verification reminder]`, `[tool-sequence]`, `[Overseer:]`, `[reversibility]` |
| `injectGuidance(msg)` / `guidanceEmit(msg)` / direct `contextManager.Add(user)` | a synthetic `user` message mid-turn | `[error-compounding]`, `[error-rush]`, `[reckless-exec]`, `[pivot-decision]`, "Progress checkpoint: iteration N/M" |

By late 2026 the stream had become noise: in one real session a dozen
reminders fired for a change that was building and passing, several of them
contradicting each other ("stop editing, verify" vs "you have edited without
reading"). The user called them unnecessary; they were removed.

## What was removed

Everything whose *only* output was a nudge. The detector files are gone
(119 files in the follow-up alone, ~20k lines including their pinning tests).
Representative families:

- Process nagging: convergence lock, verification reminder, test-coverage gap,
  effect ledger, build idempotency, verification scope narrowing, todo drop,
  orphaned file, working-tree invalidation, search invalidation, read-validity,
  expired-read, unread-edit, diminishing returns, bare-edit streak,
  feedback loop.
- Error-psychology: error cascade, error compounding, error rush, strategy
  fixation, solution fixation, strategy exhaustion/stagnation, pivot decision,
  fix cascade, recurring error, error regression, fix amnesia, error strategy
  loop, compounding failure, failure mode, error propagate, permission-deny
  streak, loop detection, tool fallback, error classifier guidance.
- Behavioural coaching: overseer, LLM overseer, tool sequence, observation
  contract, reckless exec, reversibility, attention fragment, exploration
  fragmentation, action annihilation, info scent, query convergence,
  constraint violation/amnesia nudges, subgoal track, attempt brief,
  foresight calibration, success declaration, criteria drift, reasoning
  redundancy, premature termination, futile cycle, monorepo scope sprawl,
  delegation orchestration nudges, action hedging, compounded uncertainty,
  spiral hallucination, serial read, batch coupling, edit oscillation,
  scope drift recurrence nudges, cache-efficiency storms, env drift,
  progress checkpoints, plan suggestions, latency hints, argument-size hints,
  missing-test-companion and debug-statement reminders.
- Assistant-text analysis (per turn, `chat_response_processing.go`):
  truncation claims, circular reasoning, contradiction, scope creep, premature
  abstraction, capability boundary, plan abandon/drift, reproducer lifecycle,
  tool-target mismatch, outcome misattribution, constraint audit/follow-up,
  unverified claims, companion files, spec gaming, complexity gate, cross-file
  impact, commit/doc-drift/draft-PR hints; plus the "ask the user" nudges for
  ambiguous or under-specified input (`checkAmbiguityPoints`,
  `maybeWarnInputUnderspec`) - the agent should pick a safe default and keep
  going, not stop to ask.

## How it was cut

1. **Wiring first.** Every `X := a.detector.check(...); a.appendGuidance(...)`
   pair and every direct `contextManager.Add(user)` nudge block was removed
   from `chat_tool_loop.go`, `conversation_loop.go` and `agent_tool.go`
   (1090 -> ~580 lines in the tool loop).
2. **Funnels second, as a guarantee.** `injectGuidance` now always reports
   "not delivered" and `guidanceEmit` returns the message list unchanged, so
   a stray call site can never reach the transcript. `appendGuidance` still
   appends - it is the funnel for the *kept* messages below.
3. **Then the dead code.** Detectors with no remaining consumer were deleted
   together with their tests. Detectors whose state is still read elsewhere
   stayed as state only: `causalAttribution` (`attributeFailure` now returns a
   `*causalSuspect` instead of rendering `[causal-attribution]`; the end-of-run
   failure attribution still consumes `lastSuspect`), `confidence`,
   `crossDetectorConsensus`, `repetition`, `driftRecurrence`, `overseer`,
   `successDeclare`/`criteriaDrift`/`reasoningRedund` (Turn Impact readers).
   Generic helpers that happened to live in deleted files moved to
   `trim_shared_helpers.go`; `tool_latency.go` stayed because adaptive
   per-tool timeouts are a feature, not a hint.
4. **Self-review** (`diff_summary_gate.go`) stays but filters `.ggcode/` and
   every `.gitignore`-matched path, and stays silent when only noise changed.
   The same `.ggcode/` exclusion applies to every other review surface - the
   **Modified files** prompt section, the adversarial evaluator's diff and
   the `review_changes` tool (`util.IsHarnessStatePath`; see
   `docs/design/context-management.md`, "Harness state never reaches a review
   surface").
5. **Guard-zone watchers** (`watcher_registry.go`) keep only the security and
   budget advisories (tainted-data influence, pseudo-directive args,
   exfiltration chain, SLO error budget). Build-idempotency, edit-coverage and
   tool-effectiveness watchers still record state but no longer speak.

## What deliberately stays

These are not coaching - they are correctness or safety signals, and each is
rare and specific:

| Message | Why it stays |
|---|---|
| destructive git command warning (`checkGitDestructive`) | pre-execution caution for `push --force`, `reset --hard`, branch deletion |
| post-write integrity check | the file on disk does not match what was written |
| non-atomic tool outcome notes (`mutate_outcome.go`) | a mutating tool may have applied twice / outcome unknown - needed to avoid double-apply |
| batch edit conflict warnings | two edits in one batch touch the same file |
| content checkers on write (hardcoded secrets, ...) | security review of written content |
| self-modification guard | editing ggcode's own source/config |
| tool-call budget stop, cancellation fills, deferred project memory | control flow, not advice |
| stop-before gates (`finalTurnEvidenceGate`, research-report gate, goal-finish gate, workflow outstanding steps, incomplete todos, self-review summary, adversarial-review gate) | fire once per turn when the agent tries to stop with work or verification outstanding |
| auto-verify / lint result feedback, low-disk, resumed-session dirty files | environment facts, not coaching |
| decision-time experience recall on failure | retrieves past *solutions* (content), not a reminder |

## Adding a new reminder? Don't.

If a situation genuinely needs the model to change behaviour, prefer fixing
the *tool result* (make the error message itself precise), the *prompt*
(a stable rule), or the *harness* (a gate that blocks). A mid-run reminder is
the last resort, and `injectGuidance` will silently drop it anyway.
