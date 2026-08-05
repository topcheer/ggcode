# Logging Intelligence - Structured Logging & Observability Code Quality

## Overview

ggcode's write-time integrity pipeline includes a **Logging Intelligence** check
that detects logging anti-patterns as the AI agent writes code. This is a
zero-LLM-cost, deterministic check that runs in <1ms per file.

## Problem

AI coding agents frequently introduce two logging anti-patterns not covered by
existing checks:

1. **Sensitive variables in log arguments** - `log.Printf("token: %s", token)`
   passes runtime secret values to log output. The hardcoded-secret check only
   catches *literal* values (e.g., `apiKey := "ghp_xxxx"`), not *dynamic*
   variable values. When the code runs, these values are written to log files,
   stdout, or log aggregation systems - a real data-exfiltration risk
   (OWASP A09:2021).

2. **`log.Fatal`/`log.Panic` in non-main packages** - library code calling
   `log.Fatal()` kills the entire process without giving callers any chance to
   handle the error gracefully. AI agents often copy-paste `log.Fatal` patterns
   from `main()` into helper functions.

## What It Detects

### Sensitive Variable in Log Call (Go + JS/TS)

Flags when a variable with a sensitive name (`password`, `token`, `apiKey`,
`secret`, `credential`, `bearer`, etc.) is passed as an argument to:

- Go: `log.Printf`, `log.Println`, `slog.Info`, `logger.Error`, etc.
- JS/TS: `console.log`, `console.error`, `console.warn`, etc.

**Smart filtering**: Only flags when the sensitive name appears as a *bare
identifier* (variable reference), not inside a quoted string literal. This
avoids false positives like `log.Printf("checking password for user %s", name)`.

### log.Fatal/log.Panic in Non-Main Package (Go)

Flags `log.Fatal()`, `log.Fatalf()`, `log.Panic()`, `log.Panicf()` in any Go
file that does not declare `package main`.

## Differentiation from Existing Checks

| Check | What it catches | What it misses |
|---|---|---|
| `debug-statements` | Leftover `fmt.Println`/`console.log` bare debug prints | Logging framework calls with sensitive args |
| `hardcoded-secret` | Literal secret values in source (`apiKey := "xxx"`) | Dynamic variables passed to log calls |
| `error-swallowing` | Empty `if err != nil {}` handlers | `log.Fatal` that kills the process |
| **logging-intel** (this) | **Dynamic sensitive vars in log args + Fatal in libs** | - |

## Design Decisions

- **Delta-aware**: Only flags patterns newly introduced by the current edit
  (compares occurrence counts between old and new content)
- **Multi-language**: Go, JavaScript, TypeScript, JSX, TSX
- **Exempt directories**: `testdata/`, `fixtures/`, `mocks/`, `vendor/`
- **Capped at 5 warnings** per file to prevent context overflow
- **Zero LLM cost**: Pure regex/pattern matching, no model calls
- **No false positives** on string-literal-only mentions of sensitive words

## Competitor Analysis

| Tool | Write-time? | Sensitive log args? | Fatal detection? |
|---|---|---|---|
| **ggcode** | Yes | Yes | Yes |
| Datadog/Sentry | No (runtime) | No | No |
| Semgrep | No (external lint) | Partial | Yes |
| OpenTelemetry | No (standard) | No | No |
| Claude Code | No | No | No |
| Cursor | No | No | No |

## Implementation

- **File**: `internal/agent/logging_intel_check.go`
- **Tests**: `internal/agent/logging_intel_check_test.go` (22 tests, all passing)
- **Registration**: `internal/agent/write_integrity.go` - registered as
  `logging-intel` check for `LangGo` and `LangJSTS`
