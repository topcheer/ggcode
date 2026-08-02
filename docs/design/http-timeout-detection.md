# HTTP Client Missing-Timeout Detection (Post-Write Check)

## Problem

AI coding agents frequently produce Go code that makes HTTP requests without
any timeout. The standard library's `http.Get`, `http.Post`, `http.Head`, and
`http.PostForm` all use `http.DefaultClient`, which has **no timeout** configured.
Similarly, `&http.Client{}` created without a `Timeout` field also has no timeout.

In production, missing timeouts cause:
- Goroutine leaks (each hung request keeps a goroutine alive forever)
- Connection pool exhaustion (connections are never released)
- Cascading failures when a downstream service becomes unresponsive
- Deadlock-like symptoms that are extremely hard to debug

LLMs are especially prone to this because the standard library examples use
`http.Get` directly (which looks correct), and the absence of a timeout is not
visually obvious.

## Distinction from Resource Leak Detection

This is a **different class of bug** from resource leaks (missing `Close()`):

| Check | Bug | Consequence |
|-------|-----|-------------|
| Resource leak (`checkResourceLeaks`) | `resp.Body` never closed | File descriptor exhaustion |
| **HTTP timeout** (`checkHTTPTimeout`) | **Request never returns** | **Goroutine/connection leak, indefinite hang** |

A function can pass the resource-leak check (has `defer resp.Body.Close()`) but
still hang indefinitely because there is no timeout. **Both checks are needed.**

Example that passes resource-leak but fails timeout:
```go
resp, err := http.Get(url)       // no timeout!
if err != nil { return err }
defer resp.Body.Close()           // resource leak handled
```

## Competitor Analysis

| Tool | Detection | Timing |
|------|-----------|--------|
| Claude Code | None (relies on external linters) | N/A |
| Cursor | None (lint-on-save may catch via golangci-lint) | Reactive |
| Cline/OpenHands | None | Reactive (incidents) |
| Aider | None | N/A |
| Windsurf | None | N/A |
| staticcheck | Does not flag missing timeouts (S1011 is different) | N/A |
| gosec | Partial (G107/G112 cover SSRF, not missing timeouts) | Reactive |
| **ggcode** | **AST-based inline detection** | **At write time (<1ms)** |

None provide inline detection at write time. This check provides immediate,
zero-dependency feedback using Go's standard library AST parser.

## Design

The check runs as part of the `checkWriteIntegrity` pipeline (check #22) after
every successful file write. It detects three patterns:

### Pattern 1: http.Get/Post/Head/PostForm (DefaultClient functions)

These package-level functions always use `http.DefaultClient`, which has no
timeout.

```go
// Flagged:
resp, err := http.Get("http://example.com")
```

### Pattern 2: http.DefaultClient.Do/Get/Post

Explicit use of `DefaultClient` -- same problem as above.

```go
// Flagged:
resp, err := http.DefaultClient.Get("http://example.com")
```

### Pattern 3: &http.Client{} without Timeout field

A custom client created without a `Timeout` field.

```go
// Flagged:
client := &http.Client{}
resp, err := client.Get("http://example.com")

// Not flagged (has Timeout):
client := &http.Client{Timeout: 30 * time.Second}
resp, err := client.Get("http://example.com")
```

### Delta-Aware Detection

Only patterns **newly introduced** by the current edit are reported. Pre-existing
issues are not re-flagged on every subsequent edit, reducing noise.

### Performance

- Uses `go/parser` AST analysis (the AST is already parsed once and shared with
  other Go checks)
- Runs in <1ms for typical files
- Non-blocking: advisory warnings, does not prevent writes
- Zero external dependencies (standard library only)

## Recommended Fixes

When the check fires, the agent should apply one of these fixes:

1. **Use a custom client with timeout** (recommended for most cases):
```go
client := &http.Client{Timeout: 30 * time.Second}
resp, err := client.Get(url)
```

2. **Use context-based deadline** (recommended for request-scoped cancellation):
```go
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
resp, err := client.Do(req)
```
