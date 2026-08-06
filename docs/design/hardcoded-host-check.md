# Hardcoded Host/Port Detection (sa-71)

## Overview

Detects hardcoded bind addresses and ports in server code across Go, JS/TS, and Python at write time. This is a zero-LLM-cost, AST/regex-based check registered in the post-write integrity pipeline.

## Problem

AI coding agents frequently emit server/listener code with hardcoded addresses like `:8080`, `localhost:3000`, or `0.0.0.0:5000` instead of using environment variables. This causes:

1. **Deployment inflexibility**: Port conflicts in containers and orchestration platforms
2. **Security exposure**: Unintended binding to `0.0.0.0` exposes services externally
3. **Testing friction**: Parallel tests collide on the same port
4. **12-factor violation**: Config should come from the environment

## Competitor Analysis

| Product | Inline Detection | Notes |
|---------|-----------------|-------|
| Claude Code | No | Relies on review comments |
| Cursor | No | External lint rules only |
| Cline/OpenHands | No | Reactive via incidents |
| Aider | No | No detection |
| Windsurf | No | No detection |
| gosec (G304) | Partial | File path injection, not bind addresses |
| **ggcode** | **Yes** | **Write-time AST + regex detection** |

## Detection Patterns

### Go (AST-based)

1. `http.ListenAndServe(":8080", nil)` — hardcoded port in first arg
2. `http.ListenAndServeTLS(":8443", ...)` — same for TLS variant
3. `net.Listen("tcp", ":3000")` — hardcoded address in second arg
4. `server.ListenAndServe(":9090")` — method call variant
5. Binding to `0.0.0.0` — flagged as risky wildcard bind

### JS/TS (regex-based)

1. `app.listen(3000)` — hardcoded port in `.listen()` call
2. `{ host: '0.0.0.0' }` — hardcoded host in config object
3. `{ host: 'localhost' }` — hardcoded localhost

### Python (regex-based)

1. `app.run(host='0.0.0.0', port=5000)` — Flask/Django hardcoded config
2. `PORT = 8080` — standalone port constant

## Design Decisions

1. **Dev-port allowlist**: Only flags common development ports (3000, 5000, 8080, 8443, 9090, etc.). Standard ports (80, 443) and uncommon ports are not flagged to minimize false positives.

2. **Delta-aware**: Pre-existing hardcoded addresses are not re-reported on subsequent edits — only newly introduced patterns trigger warnings.

3. **Multi-language**: Go uses precise AST analysis; JS/TS and Python use targeted regex patterns.

4. **Capped output**: Maximum 4 warnings per file with truncation notice to prevent flooding.

5. **Env var awareness**: Code using `os.Getenv()`, `process.env.PORT`, or `os.environ.get()` does not trigger warnings.

## Files

- `internal/agent/hardcoded_host_check.go` — detection logic
- `internal/agent/hardcoded_host_check_test.go` — 18 tests
- `internal/agent/write_integrity.go` — registration (1 line)

## Complexity

All functions have cyclomatic complexity under 15. Zero external dependencies. Zero LLM cost.
