# SQL Injection Detection (OWASP A03:2021)

## Design Document

### Problem

AI coding agents frequently write SQL queries using string concatenation or
`fmt.Sprintf`, creating SQL injection vulnerabilities. These bugs pass
compilation and basic testing but are exploitable at runtime. This is OWASP
A03:2021 (Injection), consistently in the top 3 OWASP risks.

### Gap Analysis

No AI coding agent detects SQL injection at write time:
- **Claude Code**: no write-time detection (relies on gosec/semgrep)
- **Cursor**: no detection (relies on external linters)
- **Cline/OpenHands**: no detection
- **GitHub Copilot**: no detection
- **Aider**: no detection
- **gosec G201/G202**: detects at lint/CI time, not write time
- **semgrep**: detects at CI time, not write time

The existing `insecure_pattern_check.go` in ggcode covers `math/rand` usage
and hardcoded passwords, but does NOT cover SQL query construction patterns.

### Approach

AST-based analysis of Go files. For each call to a database method
(`Query`, `Exec`, `QueryRow`, `Prepare`, `NamedExec`, etc.), checks if the
query argument is built via:
1. **String concatenation** (`"SELECT ..." + variable`)
2. **fmt.Sprintf interpolation** (`fmt.Sprintf("SELECT ... %s", var)`)

The safe alternative uses parameterized queries with placeholders (`?` or `$1`).

### Detection Patterns

| Pattern | Risk | Example |
|---------|------|---------|
| String concat in query arg | High | `db.Query("... WHERE x = '" + name + "'")` |
| fmt.Sprintf in query arg | High | `db.Exec(fmt.Sprintf("... WHERE y = '%s'", val))` |
| Literal string only | Safe | `db.Query("SELECT * FROM users")` |
| Parameterized query | Safe | `db.Query("SELECT ... WHERE x = ?", name)` |

### Context-Aware Methods

Context-variant methods (`QueryContext`, `ExecContext`, etc.) take
`context.Context` as the first argument, so the query string is the second
argument. The check handles this correctly.

### Design Decisions

1. **Helper function prefix**: All helpers use `sqlInj` prefix to avoid
   naming collisions with existing functions in the agent package.
2. **Max warnings capped at 4** with truncation notice.
3. **Zero LLM cost**: Pure AST pattern matching using `go/parser`.
4. **Delta-aware**: Operates on `newContent` only; doesn't flag pre-existing code.
5. **No false positives on safe patterns**: Literal-only queries and
   parameterized queries are correctly skipped.

### Files

- `internal/agent/sql_injection_check.go` (155 lines) — detection logic
- `internal/agent/sql_injection_check_test.go` (170 lines) — 14 tests
- `internal/agent/write_integrity.go` — 1 registration entry

### Complexity

All functions are well under cyclomatic complexity 15:
- `checkSQLInjection`: 7
- `sqlInjExtractMethodName`: 3
- `sqlInjGetQueryArg`: 4
- `sqlInjCheckUnsafeArg`: 4
- `sqlInjIsFmtSprintf`: 4

### Future Enhancements

- Detect `strings.Join` used to build queries
- Flag SQL keywords in non-literal string expressions
- Support JS/TS `mysql.query()` and Python `cursor.execute()` patterns
