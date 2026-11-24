# Code Index (BM25 background index for `code_search`)

> Status: implemented; loop-closure fixes in #5945 (2026-11-24). Code:
> `internal/tool/code_index_manager.go`, consumer `internal/tool/code_search.go`.

## The loop

```
            ┌──────────────────────────────────────────────────────────────┐
            │                                                              │
 agent edit ──MarkDirty──▶ dirtyFiles ──debounce 3s──▶ rebuildDirty ──▶ persistIndex (JSON, atomic rename)
 (tool loop)                   ▲                                   │
                               │                                   ▼
 TUI @-mention ◀──FilePathFuzzy┤                             loadDiskCache / doBuild (mtime hit -> reuse TF)
                               │                                   │
 periodic tick (5 min) ──scanForExternalChanges (walk, compare   │
                               against in-memory mtimes) ─────────┘   Search / WaitReady
                               │
                               └── deletions -> dirty (-1) -> removeDocFromIndex

 idle 60 min ──▶ release in-memory index (ready=false) ──Search──▶ lazyLoad (doBuild from cache)
 home dir   ──▶ disabled (never builds)
 two procs  ──▶ flock on <index>.json.lock: loser = degraded reader, reloads disk cache each tick
```

Invariants:
- **Truth is the in-memory index.** The periodic scan compares file mtimes
  against `idx.docs[*].mtime` and `budgetExcluded`, never against the disk
  cache. (Before #5945 it re-read the cache JSON every tick and, when the
  cache had been discarded for size, marked every file dirty forever.)
- **Every agent shares its registry's manager.** `NewAgent` wires
  `reg.CodeIndex()` so `MarkDirty` fires in headless / daemon / ACP / A2A /
  agentruntime too, not only in the TUI.
- **Empty is a terminal state.** A tree with nothing indexable reports
  `ready=true, empty=true`; `Search` answers `errIndexEmpty` with a hint to
  use `grep`/`search_files` instead of "being built, try again" forever.
- **Size cap is enforced incrementally.** A file that grows past 256 KB is
  dropped by the next scan + incremental rebuild (mirrors `doBuild`'s skip).
- **Cold start is not a round-trip.** `code_search` waits up to 2 s
  (`WaitReady`) for an in-flight warm build before answering "being built".
- Budgets: 50k files, 1.5M terms (`GGCODE_CODE_INDEX_MAX_TERMS`), 5 min build
  timeout; files past the budget are remembered in `budgetExcluded` and never
  re-added incrementally.

## Costs to keep in mind

- The persisted index for a large repo is tens of MB (this repo: ~28 MB) and
  is rewritten in full on every incremental rebuild (edit bursts are debounced
  to one write per 3 s). Fine for now; if it shows up in profiles, persist
  only on idle or shard by top-level directory.
- `Stop()` is never called by any lifecycle owner: a manager lives as long as
  its registry / process. That is correct for the per-process registry; do
  **not** call `Stop()` from `Agent` shutdown - in A2A the registry is shared
  by per-task agents.
