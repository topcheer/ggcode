# Context Budget Awareness Gate

## Date: 2025-01-20
## Status: Implemented
## Direction: Context Engineering
## Priority: High

## Problem

AI agents make "context-blind" tool calls: expensive operations (reading large files, broad searches, verbose build commands) executed without awareness of how much context budget remains. This causes:

1. **Context flooding**: A single `read_file` without limit at 80% fill can add 8K+ tokens, triggering forced compaction
2. **Compaction cascades**: Compaction loses critical context, causing the agent to re-read files it already saw
3. **Efficiency waste**: Frontier models degrade significantly past ~50% context fill (Chroma 2025 study)

## Research Basis

- **Anthropic "Context Engineering" (2025)**: Identifies context-blind tool calls as a top efficiency anti-pattern
- **ACE Framework (ICLR 2026)**: Context waste patterns -- including oversized tool output -- are the #1 source of agent inefficiency
- **Chroma 2025 Study**: All frontier models (GPT-4, Claude 3.5, Gemini) degrade significantly past ~50% context fullness

## Competitor Analysis

| Competitor | Pre-execution budget awareness? |
|---|---|
| Claude Code | No -- truncates large results post-hoc but no pre-execution warning |
| Cursor | No -- relies on editor context management, not applicable to CLI agents |
| OpenHands | No -- context management is reactive |
| Aider | No -- minimal tool surface, context management is manual |
| Windsurf | Proprietary cascade system, no transparent budget gating |

**Gap**: No major competitor proactively warns BEFORE execution that a tool call will be expensive relative to remaining context budget.

## Existing ggcode Coverage (Before This Work)

| Component | What it does | Gap |
|---|---|---|
| `tool_output_guard.go` | Truncates large results AFTER execution | Reactive, doesn't prevent context waste |
| `search_param_guard.go` | Checks parameter quality (broad patterns) | Context-blind -- doesn't account for fill level |
| `context_footprint.go` | Tracks per-tool context attribution | Observational, doesn't gate |

## Implementation

**Files**:
- `internal/agent/context_budget_gate.go` -- gate implementation (297 lines)
- `internal/agent/context_budget_gate_test.go` -- 19 tests
- `internal/agent/agent.go` -- struct field, init, reset (x2), check + hint injection (x2)

**Design**:
- Two-tier threshold: `cbgDangerFill` (70%) and `cbgCriticalFill` (85%)
- At danger level: hints (non-blocking, advisory)
- At critical level: alerts (stronger urgency wording)
- Fires at most 3 times per run (avoids nagging)
- Zero LLM cost -- pure deterministic pattern matching

**Tools checked**:
| Tool | Condition | Guidance |
|---|---|---|
| `read_file` | No limit at 70%+ fill | Use offset/limit, grep first |
| `read_file` | Large limit (>2000 lines) | Narrow to specific section |
| `grep` | Content mode without type/glob filter | Add filter, use files_with_matches |
| `glob` | `**` recursive at critical fill | Use more specific pattern |
| `search_files` | Default/high max_results at critical | Reduce to 5-10 |
| `code_search` | max_results > 5 at critical | Reduce to 3-5 |
| `run_command` | Build/test/cargo at high fill | Pipe to head/tail, use -count=1 |
| `multi_file_read` | >3 files at critical, >5 at danger | Read fewer files, grep first |

## Integration

The gate is integrated into the pre-execution hint pipeline in `agent.go`, alongside:
- `searchParamGuard.checkParamQuality` (parameter quality)
- `toolRedundancy.recordCall` (duplicate call detection)
- `loopDetectionInjection` (consecutive duplicate detection)

Hints are injected into tool results (both image and non-image paths) with budget awareness.

## Test Results

All 19 tests pass covering:
- Below-threshold (no hint)
- Danger-level (hint)
- Critical-level (alert)
- Each tool type (read_file, grep, glob, search_files, code_search, run_command, multi_file_read)
- Max fires cap
- Reset behavior
- Helper functions (shortPath, shortCmd, extractString, extractInt)
