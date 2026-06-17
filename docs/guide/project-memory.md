# Project Memory

## What Memory Does

Project memory gives ggcode persistent context about your project across sessions. Instead of repeating instructions every time, you write them once in a context file and ggcode loads them automatically.

## File Hierarchy

ggcode reads context files from the project root. Files higher in the list take priority:

| Priority | File | Description |
|----------|------|-------------|
| 1 | `GGCODE.md` | Project-specific instructions (primary) |
| 2 | `AGENTS.md` | Compatibility alias for `GGCODE.md` |
| 3 | `CLAUDE.md` | Compatibility alias for Claude users |
| 4 | `COPILOT.md` | Compatibility alias for GitHub Copilot users |

If multiple files exist, the highest-priority file wins.

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
