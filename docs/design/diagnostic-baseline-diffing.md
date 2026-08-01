# Diagnostic Baseline Diffing

**Status:** Implemented  
**Date:** 2026-07-31  
**Component:** `internal/tool/diagnostic_baseline.go`

## Problem

Post-edit LSP diagnostics show ALL issues in a file, including pre-existing ones the agent didn't cause. When a file already has 5 warnings before an edit, the agent sees those 5 + any new ones but cannot distinguish which are new vs. pre-existing. This:

1. **Wastes agent time** — the agent tries to fix unrelated, pre-existing warnings
2. **Dilutes signal-to-noise** — real issues introduced by the edit get buried
3. **Creates false urgency** — the agent sees "Errors (5)" and thinks its edit broke 5 things

## Solution

**Diagnostic Baseline Diffing** captures a snapshot of LSP diagnostics BEFORE each edit, then diffs post-edit diagnostics against it. Only **newly introduced** issues are shown to the agent.

### How It Works

1. **Before edit** (`edit_file`, `write_file`, `multi_edit`, `multi_file_write`): calls `CaptureDiagnosticBaseline()` with a 150ms timeout — fast because it reads the LSP server's cached diagnostics
2. **After edit** (`postEditDiagnostics()`): calls `diffAgainstBaseline()` which compares current diagnostics against the pre-edit snapshot
3. **Output**: only NEW diagnostics are shown, labeled as "[New Diagnostics — introduced by this edit]"
4. **Resolved issues**: if the edit fixed pre-existing issues, the agent gets positive feedback: "resolved N pre-existing diagnostic(s)"

### Matching Strategy

Diagnostics are matched by **(severity, message)** — not line number. This is critical because edits shift line numbers (e.g., inserting 10 lines at the top shifts everything down). Matching by message ensures a pre-existing "unused variable" warning at line 5 that's now at line 15 is correctly recognized as pre-existing.

Duplicate messages are handled with **reference counting** — if a file had 2 "unused variable" warnings before and has 3 after, only the third is reported as new.

### Fallback Behavior

If no baseline is available (LSP server unavailable, timeout, first edit on a new file), the system falls back to showing all diagnostics — identical to the previous behavior.

## Competitor Comparison

| Tool | Post-Edit Diagnostics | Baseline Diffing |
|------|---------------------|------------------|
| **ggcode** | LSP diagnostics after every edit | Yes — only shows new issues |
| Claude Code | `go vet` in verify loop | No — shows all issues |
| Cursor | Inline squiggles | No — shows all issues |
| Cline | Lint in verification loop | No — shows all issues |
| Aider | `--lint` flag | No — shows all issues |

## Configuration

Enabled by default. Controlled by the existing `postEditDiagEnabled` flag (same as post-edit diagnostics).

## Performance Impact

- **Baseline capture**: ≤150ms (reads cached LSP diagnostics, no expensive computation)
- **Diff computation**: O(n+m) where n=baseline diagnostics, m=post-edit diagnostics — typically <1ms
- **No additional LSP requests**: reuses the same diagnostics pipeline

## Files Changed

| File | Change |
|------|--------|
| `internal/tool/diagnostic_baseline.go` | New — baseline capture, diff logic, formatting |
| `internal/tool/diagnostic_baseline_test.go` | New — 11 test cases |
| `internal/tool/post_edit_diagnostics.go` | Modified — uses baseline diff when available |
| `internal/tool/edit_file.go` | Modified — captures baseline before write |
| `internal/tool/write_file.go` | Modified — captures baseline before write |
| `internal/tool/multi_edit.go` | Modified — captures baseline before write |
| `internal/tool/multi_file_write.go` | Modified — captures baselines before all writes |
