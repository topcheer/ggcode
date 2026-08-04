# Cross-File Impact Analysis

## Problem

When an AI coding agent edits a Go file (renames a function, removes a type,
changes a method signature), other files in the same package that reference
those symbols will break. The agent typically only edits the files it was
asked to change and may not realize that its modifications have cascading
effects on files it never opened.

Existing checks address related but distinct concerns:
- **Write integrity checks** validate syntax of individual files after each edit
- **Change reconciliation** detects unexpected side-effect files from shell commands
- **Batch conflict detection** warns about same-file multiple edits in one batch

None of these detect **semantic breakage in OTHER files** caused by the agent's
symbol-level edits.

## Solution

A pre-completion gate that:

1. Parses the diff of each Go file the agent edited (old content from git HEAD
   vs new content on disk)
2. Extracts exported symbols that were REMOVED or RENAMED using `go/ast`
3. Scans sibling Go files (same package directory) for references to those
   removed symbols
4. Warns the agent about files that may need updates

## Integration

The gate runs in the pre-completion pipeline, before the change reconciliation
gate. It fires at most once per run. It is advisory (injects context, doesn't
block completion).

```
// In agent.go pre-completion pipeline:
if impactMsg := a.checkCrossFileImpact(runStats); impactMsg != "" {
    // inject context and continue loop
}
```

## Competitor Analysis

| Tool       | Approach                                           |
|------------|----------------------------------------------------|
| Claude Code| Relies on `go build` to catch these (reactive)     |
| Cursor     | IDE diagnostics flag broken references in real-time|
| Cline      | No cross-file impact analysis; relies on build     |
| Aider      | Uses tree-sitter to track changes across files     |
| Devin      | Has a dependency graph but doesn't pre-warn        |

ggcode's approach is unique: **proactive, zero-LLM-cost static analysis** that
warns before the agent declares "done".

## Design Decisions

- **Go-only**: Other languages lack reliable static analysis without LSP
- **Exported symbols only**: Minimizes false positives from private helper changes
- **Same-directory siblings**: Most common breakage pattern; avoids full-module scans
- **5-second timeout**: Bounds analysis to prevent slowdowns in large repos
- **Max 20 edited files**: Large refactors skip this check (build errors suffice)
