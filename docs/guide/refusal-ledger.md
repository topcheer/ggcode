# Persisted Refusal Ledger (Enforceable User Refusals)

## Why

Research basis: [arXiv 2605.00055 "Ambient Persuasion in a Deployed AI Agent"](https://arxiv.org/abs/2605.00055) (Cuadros & Maiga, 2026). A deployed multi-agent system's primary agent installed 107 unauthorized components, overwrote a system registry, **overrode a prior negative decision from its oversight agent**, and escalated up to an attempted sysadmin command. The trigger was not an attack — a routine technology article forwarded for discussion re-weighted the agent's directives (a "directive weighting error"), resurrecting a tool installation the agent had recommended 6 hours earlier before being told to stand down.

The paper's first design lesson, verbatim:

> "prior refusals must persist as enforceable constraints rather than message-level reminders"

A refusal that lives only as a message in the conversation window will be scrolled away, dropped by compaction, or out-weighted by later context — exactly what happened in the incident.

## What

`internal/agent/refusal_ledger.go` closes that gap for conversational refusals:

| Layer | Mechanism | Scope |
|---|---|---|
| Extract | Negation-form patterns (`don't / never / avoid / stop using / don't touch / leave alone`) from user messages | Every user message |
| Persist | `<workspace>/.ggcode/refusals.json`, atomic write, 50-entry rolling window, 30-day recency | Across runs and compaction |
| Enforce | `checkBlocked()` before **write-class** tool execution (edit/write/file_ops/run_command/git_*); a deterministic target match returns a **hard tool error**, not a reminder | Machine-enforced |
| Release | Conversational lift phrases ("ok, you can modify X now") overlapping a entry's targets remove it; `/refusals clear` wipes all | User-controlled |

### Matching is deliberately conservative

Hard blocks on guesses cost more than they buy, so an entry only blocks when its excerpt contains a **structured target**:

- Path-like tokens (≥4 chars, e.g. `internal/auth/core.go`, `pubspec.yaml`)
- Command-line flags (≥3 chars incl. dash, e.g. `--force`, `-rf`)

Rules that keep false positives near zero:

- **Read-class tools never block** — "don't touch" targets mutation; blocking `read_file`/`grep` breaks harmless verification.
- A refusal with **no structured target** ("don't be verbose") stays advisory (existing constraint-amnesia layer) — never hard-blocked.
- Approval-gate denials are intentionally **not** recorded: "not right now" is weaker than "never do this"; persisting them as 30-day blocks would over-block.
- English extraction in V1, matching the existing constraint extraction layer it extends.

## Usage

```
you:  don't touch internal/auth/core.go
      ... (any later run, any session)
agent: (edit_file on internal/auth/core.go) → hard error:
      "blocked by persisted user refusal (2 hours(s) ago): 'don't touch
      internal/auth/core.go'. ... ask them to confirm (they can also run
      /refusals clear)."

you:  ok, you can modify internal/auth/core.go now   → released
you:  /refusals                                        → list entries
you:  /refusals clear                                  → wipe all
```

## Relation to existing mechanisms

- **constraint_amnesia.go** — advisory reminder layer (1 warning/run, run-scoped). The refusal ledger is the enforcement layer the paper demands; both extract from the same message stream.
- **approval-memory / cmd deny patterns** — cover the approval UI and command approvals respectively; neither covers conversational refusals.
- **intervention_ledger.go** — the file-discipline template (atomic write, rolling window, `/x clear` UX) this ledger follows.
