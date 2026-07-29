# Research: Pre-commit Diff Quality Scanner

## Direction: Automated Pre-commit Code Review

## Trend Analysis
AI-powered code review is a top trend in the AI Agent space. Tools like GitHub Copilot Review, CodeRabbit, and Cursor's built-in review all analyze code changes for quality issues. However, these tools rely heavily on LLM calls, which are slow, costly, and non-deterministic.

## Competitor Analysis
| Feature | Claude Code | Cursor | Cline | Aider | ggcode (before) | ggcode (after) |
|---------|------------|--------|-------|-------|-----------------|----------------|
| Pre-commit diff scan | No | No | No | No | No | **Yes** |
| Debug statement detection | No | No | No | No | No | **Yes** |
| Merge conflict marker detection | No | No | No | No | No | **Yes** |
| Secret scanning on commit | No | No | No | No | Post-write only | **On commit too** |
| Debugger/breakpoint detection | No | No | No | No | No | **Yes** |
| TODO/FIXME tracking | No | No | No | No | No | **Yes** |
| Test file exclusion | N/A | N/A | N/A | N/A | N/A | **Yes** |

## Gap Identified
No competitor provides a **deterministic** pre-commit quality scanner. They all rely on the LLM to self-review, which misses mechanical issues:
1. Leftover debug print statements (`fmt.Println`, `console.log`, etc.)
2. Unresolved merge conflict markers
3. Debugger/breakpoint statements (`pdb.set_trace()`, `debugger;`)
4. TODO/FIXME markers in committed code without tracking
5. Secrets that slip through (post-write scan catches some, but not all paths)

## Implementation
**Files created/modified:**
- `internal/tool/diff_scan.go` (new) — Core scanning logic
- `internal/tool/diff_scan_test.go` (new) — 18 test cases
- `internal/tool/git_commit.go` (modified) — Integration + bug fix

**Design decisions:**
- Non-blocking advisory: commit proceeds, warnings appended to result
- Only checks ADDED lines (not context or removed)
- Test files excluded from debug-stmt checks
- Issues capped at 15 to avoid context flooding
- Line numbers tracked from git hunk headers

**Bug fix:** Fixed pre-existing duplicate `branchWarning` append in git_commit.go.
