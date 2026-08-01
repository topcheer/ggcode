# Cross-Tool Rules File Compatibility

## Motivation

The AI coding ecosystem has fragmented across many tools, each with its own rules
file format. Teams using multiple agents (e.g. Cursor for IDE, ggcode for CLI,
GitHub Copilot for review) end up duplicating project conventions in multiple
files or switching tools entirely.

The industry is converging on `AGENTS.md` as an open standard (adopted by Cursor,
Windsurf, and others), but many existing repositories still use tool-specific
files like `.cursorrules` or `.github/copilot-instructions.md`.

## Design

ggcode loads rules files from other AI coding tools as **compatibility sources**,
appended after primary files so ggcode-native conventions take precedence.

### File Priority (high to low)

1. **Primary** — loaded from `~/.ggcode/` and project root:
   - `GGCODE.md` (ggcode native)
   - `AGENTS.md` (open standard)
   - `CLAUDE.md` (Claude Code)
   - `COPILOT.md` (GitHub Copilot project file)

2. **Compatibility** — loaded from project root only:
   - `.cursorrules` (Cursor)
   - `.windsurfrules` (Windsurf)
   - `.clinerules` (Cline)

3. **Subdirectory** — loaded from project subdirectories:
   - `.github/copilot-instructions.md` (GitHub Copilot)

### Scope

- Compatibility files are **project-scoped only** (not loaded from `~/.ggcode/`)
- They are **merged** with primary content — not replacing it
- They appear in the "Project Memory" system prompt hint, labeled by base name

### What This Is Not

- Not a conversion tool — we don't translate between formats
- Not a parent-directory walk — still only current directory + global config dir
- Not a replacement for `GGCODE.md` — just a fallback for cross-tool repos

## Implementation

- `internal/memory/project.go`: Added `.cursorrules`, `.windsurfrules`,
  `.clinerules` to `ProjectMemoryFilenames` and
  `.github/copilot-instructions.md` to a new `CompatibilitySubdirRules` list
  checked by `listProjectMemoryFiles()`

## Competitor Analysis

| Tool | Reads foreign rules? | Files read |
|------|---------------------|------------|
| Claude Code | Partial | `CLAUDE.md`, parent-dir walk |
| Cursor | No | `.cursorrules`, `.cursor/rules/` |
| Windsurf | Partial | `.windsurfrules`, reads `AGENTS.md` |
| Cline | No | `.clinerules` |
| GitHub Copilot | No | `.github/copilot-instructions.md` |
| **ggcode** | **Yes** | All of the above |

ggcode is the most compatible — it reads every major tool's rules file format,
making it the best "drop-in replacement" for teams migrating between tools.
