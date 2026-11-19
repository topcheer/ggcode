# Persistent Team Design (teammate cross-restart persistence)

Status: **design approved for implementation** | Date: 2026-11-19 | Prereq: #5822 (MERGED d213671c3)

## Problem

Today's persistent teammates survive *within* one ggcode process lifetime (context kept across
tasks, early-compact at 70%, done-mail, LastActiveAt — #5822). On process restart everything
except the task board is lost:

| Layer | Persisted today | Gap |
|---|---|---|
| Task board | yes (board_persist.go, per-team JSON + file lock) | — |
| Task results | yes (persistTaskResult → board) | — |
| Teammate memory/context | **no** (Agent contextManager is in-memory only) | P1 |
| Team manifest (members/roles/tools) | **no** | P2 |
| Leader inbox unread mail | **no** (done-mail is advisory) | P3, low |

## P1 — Teammate session journal (core: the "memory device")

Reuse the main agent's session machinery end-to-end:

1. **Session id per teammate**: at `SpawnTeammate`, allocate
   `sessions/<ts>-<rand>-tm-<teamID>-<name>.jsonl` (suffix convention keeps teammate journals
   distinguishable from user sessions in listers; filters by prefix `*-tm-`).
2. **Write-through**: wire the teammate's Agent contextManager to append turn records to the
   journal exactly as the main conversation loop does (user/system/inject = input turns,
   assistant/tool = output turns). The write path already exists for the main loop — add the
   same journaler hook when constructing the teammate agent in the factory closure
   (tui/repl.go:712 factory + swarm Manager.agentFactory call site).
3. **Recovery = --resume semantics**: new manager API
   `RestoreTeam(teamID)` → reads team manifest (P2), for each member replays the journal through
   the context loader (same code path as session resume), then starts `runTeammateLoop` on the
   rehydrated agent. Compaction applies the existing threshold logic on load (over-threshold
   journals get one summarize pass before the loop starts) — no new compaction code.
4. **Shutdown finalize**: on graceful shutdown (Manager.Shutdown / ShutdownTeammate), flush the
   journal footer (status marker) so RestoreTeam can distinguish clean vs crashed sessions and
   emit a WARN for crashed ones (mirrors the arm-record pattern from #5753).

### Acceptance (P1)
- Integration test: create team → spawn 2 teammates → run a task that mutates context →
  teardown manager (in-process) → RestoreTeam → assert each teammate's next task response
  references prior context (memory survived) and LastActiveAt/early-compact ratio survive.
- Journal file naming passes the `*-tm-` filter; user session listers exclude teammate journals.

## P2 — Team manifest persistence

`~/.ggcode/swarm/teams/<teamID>.json`:

```
{ "teamID": "...", "name": "...", "leaderID": "...", "createdAt": "...",
  "teammates": [ { "name": "...", "role": "...", "color": 32,
                   "allowedTools": ["read_file","grep"],
                   "sessionFile": "sessions/...-tm-....jsonl",
                   "lastActiveAt": "...", "lastResultSummary": "..." } ] }
```

- Written on: CreateTeam / SpawnTeammate / ShutdownTeammate / DeleteTeam (reuse the existing
  board persist lock discipline — same dir, same lock family).
- `lastResultSummary` (≤120 chars) is stamped from the #5822 done-mail hook — makes the manifest
  double as a durable "who did what last" dashboard even without restoring.
- **RestoreTeam UX**: `team_restore` tool (name → loads manifest + P1 journals). Manifest alone
  without a journal file = member restored with fresh memory + WARN (crashed/unlinked).

### Acceptance (P2)
- Unit: manifest round-trip (create→serialize→load→field equality), concurrent write serialized
  by lock, DeleteTeam removes manifest.
- E2E: kill-and-restore cycle preserves members *and* their memory (P1+P2 together).

## P3 — Inbox durability (deferred, explicitly out of scope for v1)

Board tasks are durable; an unread done-mail lost on restart costs one notification, not work.
Revisit only if field usage shows leader workflows depending on inbox continuity.

## Open decisions (recorded, not blocking implementation start)

1. **Memory fidelity**: full replay vs compact-on-load. Decision: compact-on-load via existing
   threshold (same as main agent resume) — no new mode.
2. **P1+P2 ordering**: ship together in one PR — RestoreTeam without manifest has no UX.
3. **Independent daemon** (teammates outliving the leader process): out of scope v1. The
   restoration story already covers the practical case (host restarts, all seats resume).

## Implementation notes

- Touch points: internal/swarm/manager.go (RestoreTeam, manifest IO), idle_runner.go (journal
  write-through hook), tui/repl.go factory (journaler wiring), tool registry (team_restore tool),
  docs/guide (persistent-teams section).
- Everything reuses existing primitives: session journal writer/loader, board lock/persist,
  compaction thresholds, #5822 hooks. No new storage format.
- Tests colocated in internal/swarm (manifest + restore unit), e2e via existing collab harness.
