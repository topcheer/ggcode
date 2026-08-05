# Time Format Layout Error Detection (Design Doc)

## Trend: Multi-Language Knowledge Transfer Errors in AI Agents

## Problem

Go uses a unique reference-time-based date formatting system
(`Mon Jan 2 15:04:05 MST 2006`) instead of the widely-used strftime/ISO-8601
tokens (`YYYY`, `MM`, `DD`, `%Y`, `%m`, `%d`) found in Python, Java, JavaScript,
and virtually every other language.

AI coding agents trained on multi-language corpora frequently generate date
layouts using the wrong token system:

```go
t.Format("YYYY-MM-DD")          // outputs literal "YYYY-MM-DD"!
t.Format("yyyy/MM/dd HH:mm:ss") // outputs literal "yyyy/MM/dd HH:mm:ss"!
time.Parse("YYYY-MM-DD", s)     // silently fails to parse correctly
t.Format("%Y-%m-%d")            // strftime tokens don't work in Go
```

The correct Go reference time mappings are:

| Other Lang | Go Token | Meaning |
|---|---|---|
| YYYY | 2006 | Year |
| MM | 01 | Month |
| DD | 02 | Day |
| HH | 15 | Hour (24h) |
| mm | 04 | Minute |
| ss | 05 | Second |
| ZZ | -0700 | Timezone offset |
| %Y | 2006 | Year (strftime) |
| %m | 01 | Month (strftime) |
| %d | 02 | Day (strftime) |

## Distinction from i18n_check

The existing `i18n_check.go` detects **hardcoded-but-correct** Go time formats
(e.g., `t.Format("2006-01-02")`) and warns about locale sensitivity. This check
detects **incorrect** layouts (e.g., `t.Format("YYYY-MM-DD")`) that produce
silently wrong output at runtime.

- i18n: `t.Format("2006-01-02")` - correct Go format, but hardcoded
- this: `t.Format("YYYY-MM-DD")` - wrong format, outputs literal text

## Competitor Analysis

| Competitor | Detects at Write Time? | Notes |
|---|---|---|
| Claude Code | No | No detection |
| Cursor | No | Lint integrations don't flag this |
| Cline/OpenHands | No | No detection |
| Aider | No | No detection |
| Windsurf | No | No detection |
| Devin | No | No detection |
| go vet | No | Does not flag wrong layout tokens |
| staticcheck | No | Does not flag wrong layout tokens |
| golangci-lint | No | Does not flag wrong layout tokens |
| GoLand IDE | Partial | Has a basic inspection, behind paywall |

This is a high-frequency, high-impact gap. No major AI coding agent or Go
linter detects this at write time.

## Implementation

**File**: `internal/agent/time_format_check.go`

**Approach**: AST-based analysis. Parse Go source, find all `time.Format()`
and `time.Parse()` calls, extract the layout string argument, and check if it
contains non-Go date format tokens.

**Detection patterns**:
- `YYYY` / `yyyy` (4-digit year from Java/JS/Python)
- `MMM` / `MMMM` (month name abbreviation)
- `MM` (2-digit month, standalone)
- `DD` / `dd` (day of month)
- `HH` / `hh` (hour)
- `EEE` / `EEEE` (day of week)
- `%Y %m %d %H %M %S` (strftime tokens)

**Features**:
- Delta-aware: only flags newly introduced wrong layouts
- Provides corrected layout suggestion via `convertLayout()`
- Caps at 2 detailed warnings + 1 summary
- Zero LLM cost (pure AST + regex)
- Zero external dependencies

**Registration**: `write_integrity.go` line 184:
```go
{Name: "time-format", Langs: []Language{LangGo}, Run: sliceCheck(checkTimeFormat)},
```

## Tests

**File**: `internal/agent/time_format_check_test.go` (11 tests)

- Detects YYYY-MM-DD in Format()
- Detects strftime tokens in Parse()
- No warning for correct Go layout (2006-01-02)
- Detects Java-style layout (yyyy/MM/dd)
- Delta-aware (no warning for pre-existing wrong layouts)
- Delta-aware (flags newly introduced wrong layouts)
- Non-Go files skipped
- Empty content handled
- Multiple correct Go layouts not flagged (RFC3339, Jan _2, etc.)
- Multiple issues capped at 2 + summary
- convertLayout basic conversions

## Complexity

All functions have cyclomatic complexity below 15.
