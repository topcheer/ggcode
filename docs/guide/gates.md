# Completion Gates (Lifecycle Gates)

## Overview

Completion gates are advisory lifecycle checks for consequential
commands. The first gate covers pull request merge/close actions: before
a protected command runs, the model is required to pass a read-only
preflight (CI green on the exact head, no outstanding change requests,
no matching hold markers) and to pin the action to the head SHA the
preflight observed. Gates never hard-block: they inject a REQUIREMENT
prompt in supervised mode and pass through untouched in bypass/cruise
modes.

Gates are generic product capability - they work for any repository with
the `gh` CLI installed and authenticated.

## When To Use

Enable gates (the default) when:

- The agent merges or closes PRs on your behalf and you want CI and
  review state verified against the PR's *current* head, not a stale one.
- You want merges pinned to a known head (`--match-head-commit`) so a
  force-push between check and merge cannot smuggle new commits in.
- You keep manual hold markers (for example a comment or label like
  `HOLD: awaiting owner`) that should stop automated merges.

Disable them when you drive merges yourself and only use ggcode for
preparation work.

## The merge_preflight Tool

Call with a PR number:

```
merge_preflight {"pr_number": 5356, "description": "preflight PR 5356"}
```

The result's first line is machine-readable JSON, followed by a human
summary and a conclusion line. Four predicates:

| Predicate | Meaning |
|---|---|
| `head_pinned` | Always true when a head resolved; the value is the head SHA to pin via `--match-head-commit` |
| `checks_green` | Every status check on the PR rollup is SUCCESS **and** the head did not move during the preflight (checks belonging to an older head fail this predicate) |
| `reviews_resolved` | No reviewer's latest review is an outstanding CHANGES_REQUESTED |
| `holds_clear` | No PR label or comment matches a configured hold regex (default: no patterns, always clear) |

Conclusion line: `PREFLIGHT: PASS` or `PREFLIGHT: HOLD(reasons)`.

A typical safe merge afterwards:

```
gh pr merge 5356 --squash --match-head-commit <head_sha_from_preflight>
```

## Configuration

All settings live under `gates.merge` in `ggcode.yaml`:

```yaml
gates:
  merge:
    enabled: true                    # default true
    commands: ["gh pr merge", "gh pr close"]  # protected command prefixes
    hold_patterns:                   # regexes; a label/comment match holds
      - "HOLD: awaiting owner"
      - "^do not merge$"
    preflight_ttl: 60s               # PASS snapshot freshness, 1s..10m
```

A command is protected when its leading tokens equal one of `commands`
and the next token is the PR number (`gh pr merge 5356`). Configuration
is validated on load: every `hold_patterns` entry must compile (errors
name the offending entry), commands must be non-empty, and the TTL must
stay within its bounds.

### LLM Dual-Channel Management

The same keys are reachable through two channels; both write the same
persisted config:

1. **Generic config tool** (channel a): read/write with dot-notation
   keys, e.g. `config {"setting": "gates.merge.hold_patterns", "value":
   "[\"HOLD: awaiting owner\"]"}`, and `config {"list": "gates"}` to
   discover current values.
2. **merge_preflight actions** (channel b): `action=describe` (current
   config, defaults marked), `action=set` with `key`/`value` (value is a
   JSON value; validated before the atomic write), `action=unset`
   (scalars reset to default; list entries are removed by `value`), and
   `action=test` with `command` (dry-run: does the command hit a
   protected pattern, and is there a fresh PASS snapshot for its PR?
   performs no writes).

## Agent-Side Behavior

In supervised mode, when a protected command (for example
`gh pr merge 5356`) is about to run and no PASS snapshot for that PR is
fresh within `preflight_ttl`, ggcode injects a REQUIREMENT prompt: run
`merge_preflight`, confirm PASS, and add `--match-head-commit` with the
returned head SHA. A fresh PASS snapshot satisfies the requirement
silently. In bypass/cruise permission modes the gate never prompts and
never blocks. The gate is prompt-injection only - execution itself is
never denied.

## Verification

- Predicate logic, CRUD, and gate wiring are covered by unit tests
  (`internal/tool/merge_preflight_test.go`,
  `internal/agent/completion_gate_test.go`,
  `internal/agentruntime/config_access_gates_test.go`,
  `internal/config/gates_test.go`).
- The TTL snapshot store is race-sensitive and is exercised
  concurrently; run that group repeatedly with
  `go test -tags goolm -race -count=20 -run TestMergeGate ./internal/agent/`.
