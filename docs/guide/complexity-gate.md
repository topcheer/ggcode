# Complexity Quality Gate

## Overview

The complexity quality gate automatically checks your edited Go files for code quality issues before the agent declares completion. It's a zero-cost, advisory-only check that helps catch technical debt early.

## How It Works

After your build passes (sync verification), ggcode runs `codehealth.Analyze()` on every `.go` file you edited during the session. If any functions exceed quality thresholds, the agent receives an advisory message suggesting refactoring.

### Quality Thresholds

| Metric | Threshold | Meaning |
|---|---|---|
| Cyclomatic complexity | >= 15 | Too many decision paths |
| Function length | > 80 lines | Likely doing too much |
| Nesting depth | > 5 levels | Hard to read and maintain |

### Behavior

- **Advisory only**: The gate doesn't block completion. It gives the agent a chance to refactor.
- **Once per run**: Fires at most once per user turn to avoid spamming.
- **Scoped**: Only scans files the agent edited — not the entire codebase.
- **Test files skipped**: `_test.go` files are excluded.

## Manual Usage

You can also run code health analysis manually via the `code_health` tool:
```
Analyze the code health of internal/agent/
```

This gives a full report with severity rankings and health scores.
