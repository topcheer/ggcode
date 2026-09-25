# Edit-Fixer: gated model repair for failed edit_file matches

Status: shipped (r96). Owner: research rotation.

## Problem

`edit_file` already carries seven deterministic fallback strategies
(line-number anchors, indent normalization, CRLF conversion, indent shift,
trailing-whitespace tolerance, fuzzy per-line match). When all of them miss,
the entire recovery burden bounces back to the main agent loop: a fresh
`read_file` (often of a large file), a reasoning turn, and a retry. That is
one of the largest per-incident context-token sinks in long sessions.

## Mechanism (arXiv 2609.00006 "Harness Engineering", LLM edit-fixer pattern)

1. `internal/tool/edit_fixer.go` exposes a process-wide hook
   `tool.SetEditFixer(EditFixerFunc)`.
2. `internal/agent.NewAgent` wires the hook to the session provider
   (`internal/agent/edit_fixer.go`): one small, bounded completion that asks
   for the corrected `old_text` in `<fixed_old_text>` tags, given a numbered
   excerpt centered on the region nearest the failed `old_text`.
3. The corrected text re-enters the **same** `resolveOldText` matching and
   uniqueness gates as a model-supplied `old_text` — a repair gets no
   shortcut around edit safety. On success the result message carries
   `(old_text auto-corrected by edit-fixer)`.

## Containment

| Gate | Value |
|------|-------|
| Per-process repair budget | 32 calls (`editFixMaxCalls`) |
| Per-input memo | one repair attempt per (path, old_text) hash; prevents retry loops |
| old_text size cap | 8 KiB |
| File size cap | 512 KiB |
| Corrected size cap | 16 KiB |
| Call timeout | 30 s (write-path lock is held during repair) |
| Kill switch | `GGCODE_EDIT_FIXER=0` (also `false`/`off`) |
| Uniqueness | corrected text must pass byte-count + lenient-recount gates |

Failure semantics are fail-closed: any repair error, timeout, empty or
unresolvable correction returns the original diagnostic error unchanged.

## Trade-offs accepted

- The repair call holds the per-path write lock for up to 30 s; parallel
  same-path edits are already serialized by design (#2327).
- Sub-agents rewire the process-wide hook to their inherited provider; the
  last `NewAgent` wins. Repair is a leaf utility, so provider identity is
  not semantically load-bearing.
- Models that drop the tags fall back to their whole reply as candidate
  `old_text`; downstream gates make noisy fallbacks fail closed.
