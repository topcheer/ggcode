# Breaking Change Detection for Exported Symbols (Check #46)

## Trend
**API Contract Stability in AI Agent Refactoring** - detecting when agent edits modify exported symbol signatures that break callers in other files.

## Problem
The #1 multi-file refactoring failure mode for AI coding agents is modifying an exported symbol (function parameters, struct fields, interface methods) without updating all callers. Competitors handle this reactively:

| Tool | Approach | Gap |
|------|----------|-----|
| Claude Code | LSP diagnostics after build breaks | Reactive - wastes an iteration |
| Cursor | In-process diagnostics after edit | Same reactive model |
| Cline/OpenHands | Post-edit build verification | Even slower feedback loop |
| Aider | Requires confirmation for multi-file changes | Manual, doesn't detect the issue |
| Windsurf | Relies on LSP + cascade | Reactive |

## Solution
A **proactive** AST-based check that compares exported symbol signatures before and after each edit. When a signature change is detected, the agent is immediately directed to search for and update all callers - catching the root cause rather than the symptom.

### Detection scope
1. **Exported functions**: parameter list change (count, types)
2. **Exported functions**: return value list change
3. **Exported methods**: same as functions (identified by receiver type)
4. **Exported types**: struct field list change (add/remove/rename/reorder)
5. **Exported interfaces**: method set change
6. **Exported type aliases**: underlying type change
7. **Exported variables/constants**: type or category (const vs var) change

### Design decisions
- **Delta-aware**: only fires when a signature actually changed between old and new content
- **Exported-only**: non-exported symbols have callers in the same package, visible to the agent
- **Zero false positives** on unchanged symbols, comment-only changes, or new symbol additions
- **Signature-based**: ignores parameter names (which can change without breaking callers)
- **Structural fingerprint**: struct/interface fingerprints capture field/method sets, not formatting
- **Non-blocking**: injects guidance into tool result, doesn't prevent the write
- **Graceful degradation**: parse errors produce no warning (no false positives)

## Files
- `internal/agent/breaking_change_check.go` - core implementation
- `internal/agent/breaking_change_check_test.go` - 14 tests
- `internal/agent/write_integrity.go` - wired as check #46

## Test coverage
14 tests covering:
- No change (identical content)
- Non-exported func modified (no warning)
- Exported func param added
- Exported func return changed
- Exported method modified
- Struct field removed
- Interface method removed
- Exported var type changed
- Exported const-to-var category change
- New exported func added (no warning)
- Comment-only change (no warning)
- Non-Go file (no warning)
- Parse error graceful handling
- Method on non-exported type (no warning)
