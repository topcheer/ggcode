# Code Review: ggcode v1.3.186..HEAD

**Reviewer**: subagent (deepseek-v4-flash, same model as main agent)
**Range**: 30 commits / ~79 files (git v1.3.186..HEAD)
**Date**: 2026-08-01

## Verdict: SHIP WITH FIXES

Review counts: CRITICAL 0 / HIGH 2 / MEDIUM 3 / LOW 3

---

## HIGH

### H1. `scan_todos` panics on empty `--git-blame` rows — empty-line `strings.Split` + no nil rows
- **Evidence**: `internal/tool/scan_todos.go`, `parseGitBlame` — `strings.Split("", "\n")` → `[""]`, caller path skips the `len(rows)==0` nil guard when rows contain a single empty string; `gitBlameCache` uses bare `sync.Map` (no singleflight).
- **Commit**: 2a1b71d1 (rewrite)
- **Why it matters**: empty/cached-partial blame line yields a 2-element row with nil diff/commit fields → nil deref in `gitBlameRow.Fields` during classification. `scan_todos` is user-facing; a panic kills the session turn.
- **Fix**: in `parseGitBlame`, `if len(rows) == 0 || (len(rows) == 1 && strings.TrimSpace(rows[0]) == "") { return nil, nil }`; guard each row `if len(parts) < 2 { continue }`.

### H2. `FileGuard` on MCP/glob read paths — nil-guard inversion makes the guard a no-op
- **Evidence**: `internal/tool/file_guard.go` — `CheckAllowed` returns nil when guard is nil; `MergeFileGuards` (line 216) returns sandbox unmodified when no effective guard merges.
- **Commit**: 2a1b71d1
- **Why it matters**: `protected_paths`/`.env` protection is a security boundary (default patterns include `.env` at line 32). Any caller constructing `NewFileGuard(nil)` or wiring guard after sandbox setup silently downgrades to no-op — feature appears enabled while doing nothing.
- **Fix**: in `MergeFileGuards`, fall back to `defaultProtectedPatterns` when guard is nil; add test that `MergeFileGuards(sandbox, nil, wd)` still blocks `.env`.

---

## MEDIUM

### M1. `modifiedFilesSection` runs two `git status` subprocesses per prompt rebuild
- **Evidence**: `internal/agentruntime/prompt.go` — `gitStatus` re-derived inside `modifiedFilesSection` (own `--porcelain` + `--short` calls, 2s timeout each) on top of gitStatus passed in by callers.
- **Commit**: a96aaf82
- **Impact**: build sites are session-start and `/style`-driven rebuilds only, per-turn impact nil, but each rebuild pays 2 extra subprocess spawns (up to 4s on slow filesystems); passed-in gitStatus becomes dead parameter at one call site.
- **Fix**: reuse existing `gitStatus` parameter; keep fallback only when empty.

### M2. New `output_style` config key is unvalidated freeform text
- **Evidence**: `internal/config/*` style handling (e987f68f), persisted via `saveConfig()` (d591fe79)
- **Impact**: unknown style names degrade silently to default; arbitrary multi-line strings can be embedded into system prompt (injection surface via repo-committed `ggcode.yaml`).
- **Fix**: validate against known style set at load; warn on unknown; reject newlines/control markers.

### M3. LSP sibling-diagnostics goroutines may outlive the request
- **Evidence**: `internal/lsp/sibling_diagnostics.go` + `internal/tool/post_edit_diagnostics.go` (c3cdb33a / 3a3fd5a4)
- **Impact**: per-file goroutines keyed on project root without documented cap; parent cancellation not propagated. Risk is resource growth on very large repos, not deadlock.
- **Fix**: bound sibling scan (max ~32 files), share parent context, early exit.

---

## LOW

- **L1**: `scan_todos` delimiter check (7b893213 R3) — verified sound, O(n) with threshold early exit.
- **L2**: Strategist `<GOAL_ACHIEVED/>` matching (fe920bf9) — exact-tag match, no case fold; `<goal_achieved/>` would loop until cap. Acceptable given prompt contract; consider defensive fold.
- **L3**: i18n keys for style/system-prompt UI (e987f68f) — all keys exist in en/zh; no missing-key panics.

---

## Summary

**Top 3 risks before next release**:
1. H1 — scan_todos empty-blame nil deref (user-triggerable panic, trivial fix)
2. H2 — FileGuard nil-guard no-op (silent security-boundary downgrade for `.env`/protected paths)
3. M2 — unvalidated output_style config (prompt injection surface via shared config)

**Note**: working tree has uncommitted changes not part of this range; check separately before tagging.
