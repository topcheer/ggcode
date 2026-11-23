# Persistent Teams

Team state survives process and host restarts. A team created in one ggcode
session can be restored in a later one with its task board, membership, and
each teammate's conversation memory intact.

## Session namespace (#5860)

The **confirmed session id is the namespace**. Teams live at
`~/.ggcode/swarm/teams/<sessionID>/<teamID>/` — the directory layout IS the
ownership; there is no separate ownership record, lease, or adoption path.

- **Binding**: a resumed session passes its id to the swarm manager at
  construction; a fresh session binds once via `ConfirmSession` (wired from
  the session-created callback in `cmd/ggcode/root.go`). Re-confirming with
  a *different* id panics — a session has exactly one namespace, forever.
- **Own session resuming** (same session id, new process): teams are rebuilt
  automatically at startup (`AutoRestoreSessionTeams`, a plain listdir of
  the namespace) — no manual `team_restore` needed.
- **A different session** in the same workspace: the team is outside its
  namespace, invisible and unrestorable. Cross-session isolation is
  structural (it's a different directory), not a policy check.
- Creating a team before the session is confirmed fails at the tool layer
  (`team_create` reports the session is not confirmed).

## What persists

| Layer | Location | Notes |
|---|---|---|
| Task board | `~/.ggcode/swarm/teams/<sessionID>/<teamID>/board.json` | file-locked JSON |
| Team manifest | `~/.ggcode/swarm/teams/<sessionID>/<teamID>/manifest.json` | identity + members + last-result digest |
| Teammate journals | `~/.ggcode/sessions/<ts>-<rand>-tm-<teamID>-<name>.jsonl` | real session files, one per teammate |

The legacy single-level store (`~/.ggcode/swarm/teams/<teamID>/`, plus the
`owner.json` lease and manifest `ownerSessionID` field) was removed in #5863.
Pre-#5863 stores should be archived (e.g. `teams.legacy-<date>`); there is no
in-place migration.

## Discovering teams

The system prompt's `## Persistent Teams` section lists the persisted teams
known **at prompt build time** (name, team id, member count, last active) -
it is a snapshot, so a team created or restored mid-session is not in it.
For the live picture call `team_list` (leader-only):

```
team_list {"description": "list teams"}
```

It prints every team visible to this session: live teams with each
member's status / current task, and persisted-but-not-restored teams with
their manifest roster (name, id, role, last active) plus the exact
`team_restore` call to revive each one. `teammate_list` only addresses a
team that is already live.

## Restoring a team

Use the `team_restore` tool (by team name or team ID):

```
team_restore {"name": "build-squad", "description": "resume yesterday's squad"}
```

Restore scans **this session's namespace** only:

1. loads the manifest and re-creates the team (members keep their original
   teammate IDs, so board task ownership is continuous),
2. replays each member's journal through the session loader (the same code
   path as `--resume`; over-threshold journals compact with the existing
   thresholds),
3. restarts the idle loops; board tasks that were `in_progress` roll back to
   `pending` as usual.

Every board change persists to `board.json` immediately (synchronous flush
on each mutation, including the teammate's own claim / complete / rollback
transitions), so a task a teammate finished before the restart comes back
as `completed` and is never re-claimed. Only `in_progress` work - by
definition unfinished - rolls back.

A member whose journal file is missing comes back with fresh memory (plus a
WARN). A journal without a clean shutdown footer (process crash, kill -9)
WARNs as crashed and is replayed best-effort.

## Same-session double-open guard

Team IDs are random (`team-<6hex>`), so two ggcode instances can never
collide on a team directory. The remaining concurrency guard is the
per-team `board.lock` (advisory flock taken while persisting board state),
which serializes two processes of the **same** session writing one board.
Between sessions there is nothing to guard — they see different
directories.

## Teammate journals and session lists

Teammate journals are ordinary session files with the `-tm-` infix in their
IDs. They are machine-owned restore artifacts: session listers
(`/session`, workspace lists, latest-resume selection) filter them out, so
they never appear as user sessions.

## Lifecycle

- Journals are written through on every turn (the same persist path as the
  main conversation loop).
- Graceful shutdown (`teammate_shutdown`, `team_delete`, or process exit)
  flushes a `clean` footer; a panic flushes `crashed`.
- `team_delete` removes the whole team directory (board, manifest) —
  a deleted team is not restorable.
