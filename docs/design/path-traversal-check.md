# Path Traversal Vulnerability Detection (sa-69)

## Research Direction
**Trend**: Security/Supply Chain - OWASP A01:2021 Broken Access Control (#1 web vulnerability)

## Problem
AI coding agents (Claude Code, Cursor, Aider, etc.) frequently generate file-access code that trusts user-controlled input without sanitization:

```go
// Vulnerable patterns LLMs commonly generate:
data, _ := os.ReadFile(dir + "/" + r.URL.Query().Get("file"))
p := filepath.Join(basePath, r.FormValue("name"))
http.ServeFile(w, r, req.FormValue("path"))
```

An attacker can supply `../../../etc/passwd` to escape the intended directory.
This is **OWASP A01:2021** - the #1 web vulnerability category.

## Competitor Analysis

| Competitor | Path Traversal Detection |
|---|---|
| Claude Code | None at write time |
| Cursor | Relies on external linters (gosec G304) |
| Cline/OpenHands | None |
| GitHub Copilot | None in suggestions |
| Aider | None |
| gosec (CI-only) | Detects G304 but only in CI pipeline, not at write time |

**ggcode advantage**: Real-time detection at write time, before code is committed.
This is a first-mover feature - no AI coding agent detects path traversal at write time.

## Gap Analysis

Existing security checks in ggcode:
- `insecure_pattern_check.go`: TLS bypass, weak crypto, SQL injection, command injection
- `hardcoded_secret_check.go`: Credentials in source
- `http_plaintext_check.go`: http:// URLs
- `hardcoded_path_check.go`: Machine-specific absolute paths (portability, not security)
- `dependency_vuln_check.go`: Known CVEs in dependencies

**Missing**: Path traversal detection - the most common and dangerous server-side
file access vulnerability. No check existed for detecting user-controlled input
flowing into file operations.

## Implementation

**File**: `internal/agent/path_traversal_check.go` (multi-language)

### Detection Patterns

1. **Explicit traversal literal**: `"../../../etc/passwd"` in path construction
2. **Go file I/O with user input + concatenation**:
   - `os.ReadFile`, `os.Open`, `filepath.Join`, etc. with `r.URL.Query().Get(...)`, `r.FormValue(...)`, etc.
   - Combined with `+` concatenation or `fmt.Sprintf`
3. **Go `http.ServeFile` with dynamic path** (non-literal argument)
4. **JS/TS**: `path.join`/`path.resolve` + `req.params`/`req.query`, `fs.readFile` + concatenation
5. **Python**: `open()` / `os.path.join` / `send_file` with `request.args`/`request.form`

### Design Decisions

- **Delta-aware**: Only flags patterns INTRODUCED by the edit (not pre-existing)
- **Multi-language**: Go, JS/TS, Python (same patterns, different APIs)
- **Max 3 warnings per check** (consistent with other security checks)
- **Zero LLM cost**: Pure text pattern matching, no AI inference
- **Low false positives**: Requires both file I/O function AND user-input indicator
  on the same line
- **Literal path exclusion**: `http.ServeFile(w, r, "static/index.html")` is safe
  (static string literal argument)
- **Max complexity**: 14 (ptScanPythonLine) - under the 15 threshold

### Registration

In `write_integrity.go`:
```go
{Name: "path-traversal", Langs: []Language{LangGo, LangJSTS, LangPython}, Run: sliceCheck(checkPathTraversal)},
```

## Files Changed

| File | Lines | Purpose |
|---|---|---|
| `internal/agent/path_traversal_check.go` | 341 | Check implementation |
| `internal/agent/path_traversal_check_test.go` | 275 | 18 tests |
| `internal/agent/write_integrity.go` | +4 | Registration |

## Test Coverage

18 tests covering:
- Go: filepath.Join, os.ReadFile concat, ServeFile dynamic, ServeFile literal (safe), explicit traversal literal, safe static path, delta-awareness, max warnings cap
- JS: path.join with user input, fs.readFile concat
- Python: open() with concat, send_file with user input, safe static path
- Edge cases: unsupported extension, empty content
- Helpers: ptContainsTraversalLiteral, detectLangExt
