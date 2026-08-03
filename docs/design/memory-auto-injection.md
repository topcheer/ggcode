# Design: Persistent Memory Auto-Injection

## Problem

ggcode saves cross-session knowledge via `save_memory` and session-end
reflection, but the system prompt only injected **memory titles** as an index.
The LLM was told to "use read_file to retrieve full content when a title is
relevant" - but in practice the LLM rarely proactively reads memory files
unless a task directly references them. This meant valuable build processes,
architecture decisions, and conventions learned in previous sessions were
effectively wasted.

## Solution

Two-tier auto-injection strategy in `appendAutoMemory`:

### Tier 1: Inline Persistent Memories

Persistent-category memories (keys ending in `-impl`, `-design`,
`-architecture`, or starting with `build-`, `release-`) are inlined directly
into the system prompt with their full content. The agent receives this
knowledge immediately without needing to call `read_file`.

Size budgets prevent context-window bloat:
- Per-entry limit: 1200 bytes (~300 tokens)
- Total inline budget: 6000 bytes (~1500 tokens)
- Oversized persistent entries fall back to the index tier

### Tier 2: Title-Only Index

Transient memories (implementation tasks, bug fixes) and evolving memories
(research, competitor analysis) are listed as title-only entries, same as
before. The agent can selectively read these when relevant.

## Category Classification

The existing curation system (`internal/memory/curation.go`) already
classifies memory entries:

| Category | Pattern examples | Injection |
|----------|-----------------|-----------|
| Persistent | `*-impl`, `*-design`, `*-architecture`, `build-*`, `release-*` | Inline |
| Evolving | `competitor-*`, `research-*`, `perf-*`, `ux-research-*` | Index only |
| Transient | `impl-task-*`, `*-fix`, `*-bug` | Index only (expired after 30 days) |
| Default | (no matching pattern) | Index only |

## Files Changed

- `internal/memory/auto.go`: New `LoadForPrompt()` method, `MemoryEntry` type
- `internal/agentruntime/prompt.go`: `appendAutoMemory` rewritten to use
  two-tier injection
- `internal/memory/auto_prompt_test.go`: Tests for inline/index splitting,
  size budget enforcement, empty dir handling
- `docs/guide/project-memory.md`: User documentation of the auto-injection
  tiers

## Competitive Context

Claude Code stores `CLAUDE.md` content and inlines it directly. Cursor
inlines `.cursorrules`. Aider uses a convention file. But none of them
automatically accumulate knowledge from past sessions and inline it — they
all require manual authoring. ggcode now combines automatic knowledge
accumulation (via reflection + `save_memory`) with automatic injection of the
most valuable entries, creating a self-improving context loop.
