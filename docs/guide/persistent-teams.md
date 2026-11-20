# Persistent Teams

Team state survives process and host restarts. A team created in one ggcode
session can be restored in a later one with its task board, membership, and
each teammate's conversation memory intact.

## Session ownership (#5837/#5838/#5839)

Teams belong to the **session** that created them (`ownerSessionID` in the
manifest):

- **Own session resuming** (same session ID, new process): teams are rebuilt
  automatically at startup (`AutoRestoreSessionTeams`) - no manual
  `team_restore` needed.
- **A different session** in the same workspace: the team is invisible and
  unrestorable from that session (cross-session isolation).
- **Legacy teams** created before session binding (no `ownerSessionID`) are
  visible to every session; an explicit `team_restore` adopts them and
  stamps the adopting session.
- **Stranded teams** whose owning session's instance is provably dead
  (dead PID on the same host, or heartbeat long expired on a foreign
  host) are adoptable the same way: an explicit `team_restore` rebinds
  ownership to the restoring session (#5847 option B). A LIVE owner's
  foreign team stays refused.

## What persists

| Layer | Location | Notes |
|---|---|---|
| Task board | `~/.ggcode/swarm/teams/<teamID>/board.json` | pre-existing (file-locked JSON) |
| Team manifest | `~/.ggcode/swarm/teams/<teamID>/manifest.json` | identity + members + last-result digest |
| Owner lease | `~/.ggcode/swarm/teams/<teamID>/owner.json` | cross-instance safety (see below) |
| Teammate journals | `~/.ggcode/sessions/<ts>-<rand>-tm-<teamID>-<name>.jsonl` | real session files, one per teammate |

## Restoring a team

Use the `team_restore` tool (by team name or team ID):

```
team_restore {"name": "build-squad", "description": "resume yesterday's squad"}
```

Restore:

1. loads the manifest and re-creates the team (members keep their original
   teammate IDs, so board task ownership is continuous),
2. replays each member's journal through the session loader (the same code
   path as `--resume`; over-threshold journals compact with the existing
   thresholds),
3. restarts the idle loops; board tasks that were `in_progress` roll back to
   `pending` as usual.

A member whose journal file is missing comes back with fresh memory (plus a
WARN). A journal without a clean shutdown footer (process crash, kill -9)
WARNs as crashed and is replayed best-effort.

## Cross-instance safety (#5829)

Team IDs are random (`team-<6hex>`), so two ggcode instances on one machine
can never collide on a board directory, and a fresh `team_create` never
lands on an existing directory — inheriting a foreign board is always an
explicit `team_restore` decision.

Each team directory carries an owner lease (`owner.json`: boot ID, PID,
hostname, heartbeat refreshed on every persist) as a **machine-level liveness guard**. Restore
is refused only while the lease holder is provably alive: on the same host,
a live PID (different from this process) means alive, and a **dead PID
means the owner is gone regardless of how fresh the heartbeat is**; the
heartbeat freshness fallback (5 minutes) applies only to foreign hosts,
where no PID probe is possible. Self-PID leases (an exec-style restart
keeps the PID while minting a new boot ID) and dead/legacy leases are
inherited and re-stamped.

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
- `team_delete` removes the whole team directory (board, manifest, lease) —
  a deleted team is not restorable.
