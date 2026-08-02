# Diff Summary Self-Review Gate

## Problem

When the agent finishes a coding task, it returns to the user without a holistic view of all its changes. Existing systems provide fragmented views:

- **compactDiff**: per-edit line-level diffs (micro view, lost in conversation history)
- **changeReconcile**: only flags unexpected side-effect files (negative check)
- **fulfillmentGate**: checks IF work was done (boolean check)
- **scopeDrift**: warns when too many files are edited (quantity check)

None provide the agent with a consolidated "here's everything you changed" summary for self-review before declaring completion. Claude Code, Cursor Composer, and Windsurf all surface consolidated change summaries.

## Design

A new pre-completion gate (`checkDiffSummaryGate`) runs as the **last gate** before the agent exits (after complexity gate and change reconciliation). It:

1. Runs `git diff --stat HEAD` (fast, <50ms even for large repos)
2. Filters to non-trivial file changes (excludes the summary line)
3. Only fires when 2+ files changed (single-file edits already have per-edit diffs)
4. Injects a compact per-file summary with insertions/deletions
5. Fires once per run (tracked via `diffSummaryState.fired`)

The gate is positioned after sync verify and change reconciliation, so the agent has already confirmed builds pass and no unexpected side effects exist. The summary gives the LLM a final opportunity to catch:
- Incomplete edits (file listed but unusually small diff)
- Accidental deletions (file with only deletions)
- Leftover debug code (file that shouldn't have been touched)

## Gate Ordering (exit point)

1. Incomplete todo check
2. Fulfillment gate
3. Companion file guard
4. Sync verify (build/test)
5. Complexity quality gate
6. Change reconciliation gate
7. **Diff summary self-review gate** (NEW)
8. Return to user

## Configuration

No configuration needed. The gate is always active, fires deterministically, and has zero LLM cost.
