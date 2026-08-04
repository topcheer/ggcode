# Project Auto-Profiling

## Overview

ggcode automatically detects the project type, build system, frameworks, and key
files when a session starts. This information is injected into the system prompt's
Environment section, allowing the agent to use correct build/test commands from the
first interaction without trial-and-error.

This feature is inspired by Claude Code's `CLAUDE.md` auto-generation and Cursor's
`.cursorrules` project context, but is fully deterministic and zero-LLM-cost.

## How It Works

At session start, `BuildSystemPrompt` calls `DetectProjectProfile(workingDir)` which
scans the working directory root for well-known marker files:

| Marker File | Detected Language/Tool |
|---|---|
| `go.mod` | Go |
| `package.json` | JavaScript/TypeScript (+ framework detection) |
| `tsconfig.json` | TypeScript |
| `Cargo.toml` | Rust |
| `pyproject.toml` / `requirements.txt` / `setup.py` | Python |
| `pom.xml` | Java/Kotlin (Maven) |
| `build.gradle` / `build.gradle.kts` | Java/Kotlin (Gradle) |
| `Gemfile` | Ruby |
| `CMakeLists.txt` | C/C++ |
| `pubspec.yaml` | Dart/Flutter |
| `Package.swift` | Swift |
| `Dockerfile` / `docker-compose.yml` | Docker |

### Makefile + Build Tags

For Go projects, if a `Makefile` is present and contains `goolm` or `TAGS`, the
detected build/test commands automatically include the appropriate `-tags` flag.

### Framework Detection

For Node.js projects, the agent inspects `package.json` dependencies to detect:
React, Vue, Next.js, Svelte, Express, Wails.

For Rust projects, `Cargo.toml` dependencies are checked for tokio, Actix, Axum.

### Package Symbol Maps

ggcode injects a compact symbol map into the system prompt at session start.
This gives the agent structural awareness of the codebase without any tool
calls, similar to Aider's "repo map" but zero-cost.

- **Go**: exported types and functions parsed via `go/ast` (`project_symbols.go`)
- **TypeScript/JavaScript**: exported functions, classes, interfaces, types, and
  constants parsed via lightweight regex (`multilang_symbols.go`)
- **Python**: top-level public functions and classes (no leading underscore)
  parsed via lightweight regex (`multilang_symbols.go`)

All languages share the same constraints:
- Time budget of 200ms
- Directory depth 0-2
- Max 25 packages shown, 10 symbols per package
- Test files and generated artifacts are skipped

### Monorepo Detection

Common monorepo indicators are detected: `workspaces` in `package.json`,
`pnpm-workspace.yaml`, `lerna.json`, `nx.json`.

## Example Output

For a Go project with a Makefile using build tags, the system prompt's Environment
section will include:

```
## Project Profile (auto-detected)
- Languages: Go
- Build system: Make
- Build command: go build -tags goolm ./...
- Test command: go test -tags goolm ./...
- Key files: go.mod, Makefile
```

## Configuration

This feature is always enabled and requires no configuration. If no marker files
are found, the project profile section is simply omitted from the system prompt.

## Implementation

- `internal/config/project_profile.go` - detection logic and formatting
- `internal/config/config.go` - integration into `BuildSystemPrompt`
