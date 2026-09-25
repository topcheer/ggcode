# Skill Registry Client (`#registry:`)

## Motivation

Public skill registries (skills.sh hosting 18k+ skills per arXiv 2607.00911,
"From Registry to Repository: How AI Agent Skills Are Written, Adapted, and
Maintained", 2026) made skills an emerging unit of reuse in agent-based
software engineering. ggcode already had `.ggskill` export/import, versioning
and dependency constraints — but installing a community skill required
knowing the exact bundle URL. There was no discovery surface, no
install-by-name, and no integrity verification.

## User-Facing Surface

New `skill` tool prefix `#registry:` with three subcommands:

```
skill "#registry:search <query>"              # keyword search, name matches rank first
skill "#registry:install <name>[@<version>]"  # install; bare name = latest
skill "#registry:list"                        # list all entries
```

The registry source comes from the `GGCODE_SKILL_REGISTRY` environment
variable: an `http(s)://` URL or a local path to the index document (offline
teams / testing). Unset → actionable error naming the variable.

## Index Format (open spec)

A single JSON document:

```json
{
  "name": "team-registry",
  "updated_at": "2026-01-02T15:04:05Z",
  "skills": [
    {
      "name": "commit-helper",
      "description": "Generates conventional commits",
      "version": "1.2.0",
      "url": "https://example.com/skills/commit-helper.ggskill",
      "sha256": "<hex sha256 of the .ggskill file>"
    }
  ]
}
```

- Multiple entries with the same `name` express multiple versions; the
  highest version satisfying the requested constraint wins
  (`commands.CompareVersions`, npm-style operators from
  `commands.ParseDependency` reused — `name@>=2.0`, `name@1.2.3`, ...).
- `entry.name` should match the bundle's `manifest.json` `name`; the
  installed skill name is derived from the bundle manifest (self-describing).

## Security / Integrity

Inherited from the existing `#import` pipeline unless noted:

- **SSRF guards** (`openSkillSource`): private/loopback hosts refused, DNS
  rebinding checked, redirects re-checked — applies to both the index
  document and the bundle download.
- **Size caps**: index ≤ 4 MB; bundle ≤ 16 MB (same as `#import`).
- **sha256 verification**: when the index provides `sha256`, the downloaded
  bundle is hashed before extraction; mismatch → hard failure, *nothing*
  installed (fail-closed supply-chain guard).
- **Path traversal protection**: extraction goes through `importSkill`
  unchanged (absolute paths / `..` components rejected).
- **Overwrite transparency**: installing over an existing skill reports
  `replaced previously installed version X` / `already installed at this
  version`; local state is never silently hidden.

## Implementation Map

| File | Role |
|------|------|
| `internal/tool/skill_registry.go` | index fetch/parse, search ranking, version selection, checksum verify, install via staged temp bundle |
| `internal/tool/skill.go` | `#registry:` dispatch + tool description |
| `internal/tool/skill_registry_test.go` | 14 tests (normal / boundary / failure paths) |

Install reuses `importSkill` byte-for-byte by staging the verified download
into a temp `.ggskill` file — zero duplication of extraction logic.

## Non-Goals

- No registry publishing (export → upload remains out of scope).
- No global default registry URL (no ggcode-operated registry yet); teams
  and future ecosystem work configure their own.
- No index caching (each operation fetches fresh; index is tiny).
