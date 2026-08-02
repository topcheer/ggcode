# LSP Rename Blast-Radius Warning

## Problem

When `lsp_rename` is invoked on an exported symbol (function, type, constant),
the LSP server (e.g. gopls) returns workspace edits that may span many files.
The tool previously applied all edits silently, only reporting a flat list of
file paths. This made it easy for the agent to unknowingly rename a widely-used
symbol and modify dozens of files without understanding the scope.

Competitors handle this differently:
- **Cursor**: shows a rename preview in the IDE before applying
- **Claude Code**: shows a preview with file count before committing changes
- **Devin**: includes changed file list in post-completion summary

## Solution

`applyLSPFileEdits` now counts the number of distinct files and total edits.
When the file count exceeds `renameBlastRadiusThreshold` (currently 3), the
result includes a prominent `WARNING:` line:

```
WARNING: High blast-radius rename -- 5 files changed (12 total edits).
This rename touched many files. Verify all changes are correct before proceeding.

Applied workspace edits:
internal/foo/bar.go (3 edits)
internal/foo/baz.go (2 edits)
...
```

This gives the agent (and the user via tool output) immediate visibility into
the scope of the rename, enabling informed decisions about whether to proceed
or revert.

## Configuration

The threshold is hardcoded as `renameBlastRadiusThreshold = 3` in
`internal/tool/lsp.go`. This is intentionally conservative -- most renames
affect 1-2 files (same package), so 3+ files reliably signals a cross-package
or exported symbol rename.

## Testing

- `TestApplyLSPFileEdits_BlastRadiusWarning`: verifies warning appears for 4 files
- `TestApplyLSPFileEdits_NoWarningForSingleFile`: verifies no warning for 1 file
