# Monorepo Package Scoping Intelligence

## Overview

When working in a monorepo with many packages or services, AI coding agents
frequently operate across the entire workspace when the task is actually scoped
to one or two packages. This wastes context, slows down builds/tests, and can
introduce unintended cross-package side effects.

The **Monorepo Package Scoper** is a zero-LLM-cost intelligence module that
detects monorepo structure and warns the agent when it starts editing files
across too many packages without apparent cross-package intent.

## How It Works

1. **Structure Detection** (run start): Scans the workspace root for monorepo
   markers (`pnpm-workspace.yaml`, `lerna.json`, `nx.json`, `turbo.json`,
   `rush.json`) or detects 3+ subdirectories with their own package manifests
   (`package.json`, `go.mod`, `Cargo.toml`, `pyproject.toml`, etc.).

2. **Package Classification** (per edit): For each file edited, classifies it
   into a top-level package directory. Cross-cutting directories (`shared`,
   `common`, `lib`, `utils`, `internal`, `config`, etc.) are excluded since
   they are legitimately shared across packages.

3. **Scope Sprawl Warning** (per iteration): If the agent edits files across
   3+ distinct package directories in a single run, injects a one-time advisory
   hint asking the agent to confirm the scope is intentional and consider
   package-specific paths and build/test commands.

## Example Output

```
[monorepo-scope] Editing across 4 packages (users, orders, billing, shipping).
If this task is scoped to fewer packages, consider using package-specific paths
and build/test commands to avoid cross-package side effects. Confirm the scope
is intentional.
```

## Design Principles

- **Advisory, not blocking**: The hint is injected as context; the agent can
  proceed if the cross-package edits are intentional.
- **Fires once per run**: Avoids nagging on legitimate multi-package refactors.
- **Zero LLM cost**: Pure filesystem heuristics, no model calls.
- **Cross-cutting aware**: Shared/common/lib directories don't count toward
  sprawl detection.
- **Monorepo-aware only**: In single-package repos, the module stays dormant.

## Supported Monorepo Markers

| Marker | Tools |
|--------|-------|
| `pnpm-workspace.yaml` | pnpm |
| `lerna.json` | Lerna |
| `nx.json` | Nx |
| `turbo.json` | Turborepo |
| `rush.json` | Rush |
| 3+ subdirs with manifests | Generic fallback |

## Package Manifests Detected

`package.json`, `go.mod`, `Cargo.toml`, `pyproject.toml`, `setup.py`,
`pom.xml`, `build.gradle`, `build.gradle.kts`

## Configuration

This feature is automatic and requires no configuration. It activates only in
detected monorepo workspaces.
