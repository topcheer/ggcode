# Exported Doc Comment Check

## Overview

The **exported-doc** check is a write-time documentation intelligence gate that
detects newly-added Go exported identifiers (functions, types, variables,
constants) that lack godoc comments. This mirrors the golint/revive
"exported should have comment" rule, but fires at write time on the delta.

## Why It Matters

Go convention (and golint/revive) requires every exported identifier to have a
documentation comment starting with the identifier's name. Missing docs on
exported API surface is a common code review finding and degrades the quality
of generated API documentation (`go doc`, pkg.go.dev).

### Competitor Comparison

| Tool | When | Scope | Diff-aware |
|------|------|-------|------------|
| **ggcode exported-doc** | **Write-time** | **New exports only** | **Yes** |
| golint / revive | Post-hoc CI scan | All exports | No |
| Swimm | Post-hoc | Code-doc sync | Partial |
| Mintlify | Post-hoc | AI doc generation | No |
| CodeRabbit | PR review | PR diff | Partial |

The key differentiator: **write-time detection on the delta**. Instead of
flooding the agent with all pre-existing undocumented exports, only newly-added
exports introduced by the current edit are flagged. This keeps the signal
actionable.

## How It Works

1. **AST analysis**: Parses the new file content using `go/parser` with
   `parser.ParseComments` to associate doc comments with declarations.
2. **Delta comparison**: Parses the old content to identify which exports were
   already missing docs before this edit. Only **newly-introduced** undocumented
   exports trigger warnings.
3. **Smart filtering**:
   - Test files (`_test.go`) are skipped - test helpers don't need godoc.
   - `main` packages are skipped - their exports aren't part of a public API.
   - Methods on unexported receiver types are skipped - not externally reachable.
   - Single-spec declarations accept doc on the `GenDecl` or the spec.
   - Multi-spec grouped declarations require per-spec docs.

## Registration

Registered in `write_integrity.go` as `exported-doc` with `LangGo` filter:

```go
{Name: "exported-doc", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
    if ctx.GoAST == nil {
        return nil
    }
    return checkMissingExportedDocsAST(ctx.FilePath, ctx.OldContent, ctx.GoAST)
}},
```

## Example Warning

```
Exported function "ExportedFunc" was added without a godoc comment.
Go convention requires exported identifiers to be documented (golint/revive).
Add a comment starting with "ExportedFunc" above the declaration.
```

## Performance

- **Zero LLM cost**: Pure AST analysis via `go/parser`.
- **Reuses pre-parsed AST**: Leverages `CheckContext.GoAST` from the shared
  check pipeline - no redundant parsing of the new content.
- Old content parsing uses a fresh `parser.ParseFile` with comment mode.
- Warnings capped at 3 per write to avoid noise.

## Files

- `internal/agent/doc_exported_check.go` - implementation (197 lines)
- `internal/agent/doc_exported_check_test.go` - 12 tests
- `internal/agent/write_integrity.go` - 1 registration entry
