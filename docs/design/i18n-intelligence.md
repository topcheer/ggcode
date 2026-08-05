# Internationalization (i18n) Intelligence -- Write-Time Detection

## Problem

i18n shift-left is a 2025-2026 industry trend. EU Digital Sovereignty Act
requires multilingual support; Chinese/Japanese/Arabic markets need
locale-specific handling. Traditional tools (i18next, FormatJS, axe-core)
are runtime-only -- they catch i18n issues only during QA or production,
when fixing them is expensive.

**No AI coding agent detects i18n anti-patterns at WRITE TIME.**

## Competitor Analysis

| Tool | i18n Detection | When | Approach |
|------|---------------|------|----------|
| GitHub Copilot | Occasional hints | Inline | Extension-based, inconsistent |
| Cursor | None | -- | -- |
| Cline | None | -- | -- |
| Claude Code | None | -- | Relies on agent judgment |
| i18n-ally | Key extraction | Editor | Good key management, no anti-pattern detection |
| eslint-plugin-formatjs | Some | Runtime | Requires setup, FormatJS-only |
| **ggcode** | **5 check categories** | **Write time** | **Deterministic, zero-LLM-cost, all frameworks** |

## Implementation: `internal/agent/i18n_check.go`

Registered in the post-write integrity check pipeline (`write_integrity.go`)
for LangJSTS + LangGo files.

### Checks (all deterministic, zero LLM cost)

1. **Locale-sensitive methods without locale argument** -- Detects
   `.toLocaleDateString()`, `.toLocaleTimeString()`, `.toLocaleString()`
   called with no arguments. Without a locale, these use the runtime
   default, producing inconsistent output across regions.

2. **Intl APIs without locale** -- Detects `new Intl.NumberFormat()` and
   `new Intl.DateTimeFormat()` called without a locale argument.

3. **Hardcoded date format strings** -- Detects common non-localized date
   format patterns (`YYYY-MM-DD`, `MM/DD/YYYY`, `%Y-%m-%d`, etc.) in
   string literals across moment.js, date-fns, strftime, and .NET styles.

4. **Hardcoded currency symbols** -- Detects currency symbols (€, £, ¥,
   ₹, ₽, ₩, $) embedded in string literals or concatenation contexts.
   Currency position and symbol vary by locale.

5. **Go time.Format() hardcoded layout** -- Detects Go-specific
   `time.Format("2006-01-02")` patterns that hardcode locale-sensitive
   date/time formats.

### Design Decisions

- Uses `\x{NNNN}` Unicode escape syntax in Go regexp (Go regexp doesn't
  support `\u` escape sequences)
- Backtick character built via const `bt` constant for regex concatenation
- Currency symbols checked via Unicode code points, not literal characters
  (avoids encoding issues in source files)
- Warning cap at 8 with truncation notice (follows a11y pattern)
- Per-method/per-token deduplication to avoid noise
- Dollar sign detection limited to concatenation/return contexts to
  minimize false positives (template literal `${}` is excluded)

### Files

- `i18n_check.go` (287 lines) -- implementation
- `i18n_check_test.go` (302 lines) -- 29 tests, all passing
- 1 line in `write_integrity.go` -- registration

### False Positive Mitigation

- Locale methods with `undefined` as first arg are not flagged (common
  pattern for using default locale with options)
- Intl APIs with any argument are not flagged
- Non-dollar currency symbols only flagged inside string literals
- Dollar sign only flagged in concatenation/return contexts
- Non-JS/Go files skipped entirely

## Future Backlog (not implemented)

- RTL (Right-to-Left) layout support detection for Arabic/Hebrew
- Translation key consistency checking (missing keys across locale files)
- String concatenation for user-facing messages (should use templates)
- Hardcoded timezone detection in timezone-sensitive code
- Pluralization rule violations (e.g., `"1 items"` instead of proper plural rules)
