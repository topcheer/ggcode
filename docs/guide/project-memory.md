# Project Memory

## What Memory Does

Project memory gives ggcode persistent context about your project across sessions. Instead of repeating instructions every time, you write them once in a context file and ggcode loads them automatically.

## File Hierarchy

ggcode reads context files from the project root. All files are loaded and merged — each serves a different purpose:

| File | Source | Description |
|------|--------|-------------|
| `GGCODE.md` | ggcode | Project-specific instructions (primary conventions) |
| `AGENTS.md` | Open standard | Agent-specific instructions (used by TeamClaw workers) |
| `CLAUDE.md` | Claude Code | Claude-specific instructions |
| `COPILOT.md` | GitHub Copilot | GitHub Copilot-specific instructions |
| `.cursorrules` | Cursor | Cursor rules file (compatibility) |
| `.windsurfrules` | Windsurf | Windsurf rules file (compatibility) |
| `.clinerules` | Cline | Cline rules file (compatibility) |
| `.github/copilot-instructions.md` | GitHub Copilot | Copilot instructions (compatibility) |

## Nested Discovery (Monorepos)

Following the [AGENTS.md](https://agents.md) open standard, ggcode discovers
nested memory files inside monorepos. When the agent works on a file, it looks
for memory files from that file's directory up to the repository root:

```
repo/
├── AGENTS.md              # repo-wide conventions
└── packages/
    └── api/
        ├── AGENTS.md      # API-specific conventions (takes precedence)
        └── src/
            └── main.go    # editing this file discovers both files above
```

Rules:

- **Closest wins** — memory files closer to the file being edited are applied
  after (and therefore override) repo-level files on the same topic.
- Discovery stops at the repository root (the directory containing `.git`).
  Memory from unrelated ancestor directories is never loaded.
- Discovered files are injected lazily, the first time the agent touches a
  file in that subtree, and each file is injected at most once per session.

## What to Put in These Files

- **Coding standards** — style rules, naming conventions
- **Architecture notes** — module layout, key design decisions
- **Common patterns** — how errors are handled, test structure
- **Build commands** — how to build, test, and run the project

```markdown
# GGCODE.md

## Build
- `npm run build` — compile TypeScript
- `npm test` — run test suite

## Conventions
- Use named exports, not default exports
- All functions require JSDoc comments
- Error handling: throw typed errors, never return null
```

## Auto-Loaded

ggcode reads these files automatically on startup — no flags or commands needed.

## Cross-Tool Compatibility
ggcode automatically reads rules files from other AI coding tools, so you can
use the same project across multiple agents without duplicating configuration:

- `.cursorrules` (Cursor)
- `.windsurfrules` (Windsurf)
- `.clinerules` (Cline)
- `.github/copilot-instructions.md` (GitHub Copilot)

These are loaded after primary files (GGCODE.md, AGENTS.md, etc.), so your
ggcode-native conventions always take precedence.

## Global Memory

`~/.ggcode/GGCODE.md` applies to **all** projects. Use it for personal preferences and cross-project conventions.

```
~/.ggcode/GGCODE.md       # global — applies everywhere
./GGCODE.md               # project — overrides global for this repo
```

## Save Memory Tool

Skills and the agent can persist structured memory via the `save_memory` tool:

```
save_memory(key="build-process", content="Run 'make test' before committing")
```

Memory is scoped:

| Scope | Storage | Applies to |
|-------|---------|------------|
| `project` | Per-project | Current project only |
| `global` | Shared | All projects |

Prefer `project` scope unless the knowledge is truly universal.

### Deleting Memories

The agent can remove outdated or incorrect memories via the `delete_memory` tool:

```
delete_memory(key="old-build-process", scope="project")
```

This gives the agent full lifecycle control: save, read, and delete. Only
auto-saved memory entries can be deleted - project bootstrap files (GGCODE.md,
AGENTS.md, etc.) are not affected.

### Automatic Garbage Collection

At session start, ggcode runs garbage collection on the memory directory. This
physically removes files that the curation logic has already filtered out:

- **Expired transient entries**: implementation task logs older than 30 days
- **Superseded evolving entries**: older versions of research/analysis that
  have been deduped (e.g. `competitor-analysis-2026-07-01-r1` is removed when
  `competitor-analysis-2026-07-13-r3` exists)

GC is best-effort and never blocks session startup. It prevents the memory
directory from growing unbounded across hundreds of sessions.

## Auto-Injection: How Memory Reaches the Agent

ggcode automatically injects saved memory into the system prompt at session
start, using a two-tier strategy:

**Tier 1 - Inline (persistent memories):**
Entries classified as *persistent* (architecture decisions, build processes,
design docs - keys ending in `-impl`, `-design`, `-architecture`, or starting
with `build-`, `release-`) are inlined directly into the system prompt. The
agent has immediate access to their full content without needing to call
`read_file`. This ensures critical project knowledge from previous sessions is
always available.

**Tier 2 - Index (transient and evolving memories):**
Entries classified as *transient* (implementation tasks, bug fixes) or
*evolving* (research, competitor analysis, performance benchmarks) are listed
as title-only entries. The agent can selectively `read_file` these when a
title is relevant to the current task. Transient entries older than 30 days
are automatically expired.

**Size budgets:**
- Per-entry inline limit: ~1200 bytes (~300 tokens)
- Total inline budget: ~6000 bytes (~1500 tokens)

If a persistent entry exceeds the per-entry limit, it falls back to the
title-only index. This keeps the system prompt small while ensuring the most
valuable knowledge is always in context.

## Experience Case Bank (Case-Based Memory)

Beyond the rolling run-insights blob, ggcode keeps a **case-based experience
store** (Memento-style, arXiv:2508.16153): each reflective run is distilled
into a per-task *case* — the task, what the agent actually did (turns, tools,
files, commands), and the outcome — stored under
`.ggcode/memory/experience/` as one markdown file per case.

At the start of every run, the agent retrieves the up-to-3 most relevant
cases for the new task (lexical IDF scoring, no embeddings) and injects them
once as a `## Relevant Experience` system block:

```
Past experience with similar tasks in this project (case-based memory):
- [outcome: success] Task: fix flaky login test in auth package
  Files touched: internal/auth/session.go, internal/auth/session_test.go
  Approach: 4 LLM turns, 12 tool calls (top: edit_file(5), run_command(4)). Edited: internal/auth/session.go. ...
Treat these as hints about what worked before — verify against current code, not blind recipe.
```

Key properties:

- **Reconsolidation**: re-running the same logical task (normalized text
  match) updates the existing case instead of duplicating it — fresh outcome
  and approach, original creation date preserved.
- **Decay with a cap**: the store holds at most 50 cases; recording beyond
  the cap evicts the oldest, so guidance tracks the codebase's current
  reality rather than stale history.
- **Zero cold-start cost**: when the store is empty or nothing lexically
  matches, nothing is injected — no prompt noise.
- **Isolation**: the `experience/` subdirectory is invisible to the regular
  memory index and `/reflect` output; it is a separate retrieval channel.

Failures are recorded too (`outcome: failed` / `partial`), which lets the
agent avoid repeating approaches that previously did not work on similar
tasks.
