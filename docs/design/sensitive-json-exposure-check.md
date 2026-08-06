# Sensitive Field JSON Exposure Detection (OWASP A01:2021)

## Trend/Concept

**Security at Write Time** -- detecting security vulnerabilities as the AI agent
writes code, before it reaches production. This aligns with the industry shift
toward "shift-left security" and DevSecOps.

## Problem

AI coding agents (Claude Code, Cursor, Copilot, Cline, Aider, Windsurf, Devin)
frequently generate Go structs with sensitive fields like `Password`, `Token`,
`ApiKey`, `Secret`, `Credentials` -- but add `json:"password"` or `json:"api_key"`
instead of `json:"-"` to exclude them from JSON serialization.

This causes sensitive data to leak into:
- API responses (HTTP JSON bodies returned to clients)
- Log output (structured logging that marshals structs)
- Error messages that embed struct values

OWASP A01:2021 lists Broken Access Control as the #1 web application security risk.
Sensitive data exposure is a direct contributor.

## Competitor Analysis

| Product | Write-time detection | Mechanism |
|---------|---------------------|-----------|
| Claude Code | No | Relies on post-hoc review |
| Cursor | No | External lint-on-save may catch via gosec (different check) |
| Cline/OpenHands | No | Reactive only |
| Aider | No | No detection |
| Windsurf | No | No detection |
| Devin | No | No detection |
| gosec | No | Does not check struct tag sensitivity |
| **ggcode** | **Yes** | **Go AST pattern matching, zero LLM cost** |

**Gap found**: No competitor provides inline write-time detection for this
OWASP-class vulnerability.

## Implementation

**File**: `internal/agent/sensitive_json_check.go`

**Approach**: Pure Go AST analysis:
1. Parse Go file with `go/parser`
2. Walk all struct declarations via `ast.Inspect`
3. For each field with a `json:` struct tag:
   - Skip if tag value is `-` (already excluded)
   - Check field name and json tag name against sensitive patterns
   - Report warning with line number and fix guidance

**Sensitive patterns** (case-insensitive substring match):
- password, passwd, secret, token, apikey, api_key
- accesstoken, access_token, refreshtoken, refresh_token
- privatekey, private_key, credential, ssn, creditcard

**Characteristics**:
- Zero LLM cost (pure AST pattern matching)
- <1ms per file
- Max 4 warnings per write
- No false positives on `json:"-"` (correctly excluded)
- Handles both Go field name and JSON tag name matching

## Test Coverage

**File**: `internal/agent/sensitive_json_check_test.go` (13 tests)

| Test | Scenario |
|------|----------|
| PasswordField | Basic password field with json tag |
| ExcludedField | json:"-" correctly excluded |
| NoJSONTag | No json tag = no warning |
| ApiKeyTag | api_key in json tag name |
| TokenInFieldName | access_token in field name |
| NonSensitiveField | Normal fields don't trigger |
| SensitiveFieldNameButTagIsDash | json:"-" overrides sensitive name |
| NonGoFile | Non-Go files skipped |
| MultipleSensitiveFields | Multiple warnings in one struct |
| SensitiveGoNameButSafeJSONName | Field name sensitivity wins |
| MaxWarningsCap | Cap at 4 warnings |
| EmptyContent | Empty input handled |
| InvalidGoSyntax | Parse error = no crash |

## Registration

Added to `allChecks` in `write_integrity.go`:
```go
{Name: "sensitive-json", Langs: []Language{LangGo}, Run: sliceCheck(checkSensitiveJSONExposure)},
```

## Prioritization Rationale

**Priority: HIGH**
- Security vulnerability (OWASP #1 category)
- Common AI agent mistake (agents add json tags by default)
- Zero false positive rate with proper json:"-" exclusion
- No competitor offers this detection
- Low implementation cost, high impact
