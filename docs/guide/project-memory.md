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
