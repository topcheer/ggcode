# Learning-Artifact BOM (`ggcode bom`)

`ggcode bom` prints a **bill of materials** for everything the agent has learned: a read-only, deterministic inventory of every persisted artifact that currently shapes agent behavior, with content hash, source location, and the promotion gate each artifact passed on its way in.

It answers the audit question that no individual store can: *"what is the agent made of right now, and through which gate did each piece enter?"* (AgentBOM-style transparency for self-evolving agents; cf. execution-provenance survey arXiv:2606.04990.)

## Usage

```bash
ggcode bom                      # current workspace + user home
ggcode bom --workspace /path    # another workspace
ggcode bom --home /other/home   # non-default home
```

Output is JSON on stdout, suitable for scripting, diffing, and CI checks.

## Inventory classes

| Type | Store | Promotion gate |
|------|-------|----------------|
| `skill` | `.ggcode/skills/*/SKILL.md` (project + global) | `knight` — skill lifecycle governance |
| `skill-staged` | `.ggcode/skills-staging/*/SKILL.md` | `knight-staging` — awaiting dawn promotion gate |
| `memory` | `.ggcode/memory/*.md` (project + global) | `auto` (research-/build-/release-/preference- prefixes) or `manual` |
| `traj-insight` | `.ggcode/trajectory-learnings.jsonl` | `holdout` — shadow-holdout counterfactual delta gate |

Each entry carries:

- `name` / `type` / `scope` (`project` | `global`)
- `path` — absolute source path
- `sha256` — first 12 hex chars of the content hash
- `bytes` / `modified`
- `enabled` — skills only; honors `~/.ggcode/disabled_skills.json` (disabled skills stay listed so the BOM reflects the full learned surface)
- `promotion_gate` — the mechanism that admitted the artifact
- `items` — non-empty line count, for JSONL ledgers

The report ends with per-type `counts`. Missing stores simply contribute no entries; the command never writes anything.

## Example

```json
{
  "generated_at": "2026-10-13T17:06:30.681824Z",
  "workspace": "/Volumes/new/ggai/ggcode",
  "home": "/Users/zhanju",
  "counts": { "memory": 1493, "skill": 6, "skill-staged": 1, "traj-insight": 2 },
  "entries": [
    {
      "name": "deploy",
      "type": "skill",
      "scope": "project",
      "path": "/ws/.ggcode/skills/deploy/SKILL.md",
      "sha256": "5860ac0e4244",
      "bytes": 1495,
      "enabled": true,
      "promotion_gate": "knight"
    }
  ]
}
```

## Design notes

- **Read-only and lexical**: no LLM calls, no locks, no writes. Safe to run any time, including concurrently with a live agent.
- **Whole-ledger granularity for traj-insight**: the trajectory ledger is one store, not independent artifacts, so it is inventoried as a single entry with an item count.
- **Hashes are content addresses**: snapshot the BOM before/after a consolidation run to see exactly which learned artifacts changed.
