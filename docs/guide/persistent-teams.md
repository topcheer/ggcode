# Persistent Teams

Team state survives process and host restarts. A team created in one ggcode
session can be restored in a later one with its task board, membership, and
each teammate's conversation memory intact.

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
hostname, heartbeat refreshed on every persist). `team_restore` refuses a
team whose owner is alive (same-host PID probe, or heartbeat fresher than 5
minutes); a dead or legacy (pre-owner.json) lease is inherited and
re-stamped.

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
