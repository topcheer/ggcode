# Major Version Bump Breaking Change Detection

## Overview

ggcode detects when a dependency's **major version** is bumped in a manifest file
(go.mod, package.json, requirements.txt, Cargo.toml) and proactively warns about
likely breaking changes with specific migration guidance.

## Problem

Under Semantic Versioning, a major version change (e.g., v1.x -> v2.x) signals
**incompatible API changes**. AI agents frequently upgrade dependencies without
considering these consequences:

- **Go modules**: v2+ requires import path change (e.g., `/v2` suffix)
- **React 17->18**: `ReactDOM.render` -> `createRoot`
- **Express 3->4**: middleware/app restructuring
- **Django 3->4**: async views, removed deprecated settings
- **Pydantic 1->2**: complete rewrite with breaking API changes

## How It Works

The check is **delta-aware**: it parses dependencies from both the old and new
content of the manifest file, identifies version changes, and only fires when
a dependency's major version increases.

### Detection Flow

1. Detects manifest file by name (go.mod, package.json, etc.)
2. Parses dependencies from old and new content using ecosystem-specific parsers
3. Compares major versions for each changed dependency
4. If major version increased, emits a warning with:
   - Package name and version transition (v1 -> v2)
   - Package-specific migration guidance (for known packages)
   - Generic ecosystem-specific guidance (for unknown packages)

### Supported Ecosystems

| Ecosystem | Manifest File | Known Packages |
|-----------|--------------|----------------|
| Go | go.mod | gin, jwt, viper |
| npm | package.json | react, next, express, typescript, vite, tailwindcss, vue |
| Python | requirements.txt | django, flask, fastapi, pydantic |
| Rust | Cargo.toml | tokio, actix-web |

## Example Output

When an agent edits `go.mod` to upgrade `github.com/gin-gonic/gin` from `v1.9.0`
to `v2.0.0`:

```
[Post-write integrity check]
[Major Version Bump] github.com/gin-gonic/gin upgraded v1 -> v2 in go.mod.
Breaking changes likely under SemVer. Gin v2 changes middleware behavior and
context API. Review the migration guide at https://github.com/gin-gonic/gin/releases.
```

For unknown packages, generic guidance is provided:

```
[Major Version Bump] github.com/some/unknown upgraded v1 -> v2 in go.mod.
Breaking changes likely under SemVer. Go modules: v2+ requires import path
suffix (e.g., /v2). Update all import statements.
```

## Competitor Comparison

| Feature | ggcode | Claude Code | Cursor | Cline/OpenHands | Aider |
|---------|--------|-------------|--------|-----------------|-------|
| Write-time major bump detection | Yes | No | No | No | No |
| Migration guidance | Yes | No | No | No | No |
| Multi-ecosystem support | Yes | No | No | No | No |

## Related

- [Dependency Vulnerability Detection](dependency-vulnerability-detection.md) - detects known CVEs
- [Write Integrity Checks](getting-started.md) - overall post-write validation framework
