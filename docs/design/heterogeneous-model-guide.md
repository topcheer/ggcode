# Heterogeneous Model Selection Guide (sa-131)

## Research Basis

2025-2026 AI Agent trends (Deloitte, Machine Learning Mastery) identify **FinOps for AI Agents** as a critical frontier concept. The economic imperative is to use heterogeneous model architectures:

- **Frontier models** (Claude Opus, GPT-4) for complex reasoning
- **Mid-tier models** for standard tasks  
- **Small language models** for high-frequency execution

The **Plan-and-Execute pattern** reduces costs by 90% compared to using frontier models for everything (capable model plans, cheaper models execute).

## Implementation

### Location
- `internal/agent/heterogeneous_model_guide.go` (170 lines)
- `internal/agent/heterogeneous_model_guide_test.go` (112 lines)
- Integrated in `internal/agent/agent.go` (12 lines added)

### How It Works

#### 1. Tool Classification
Tools are categorized into 6 types:

- **Read**: `read_file`, `multi_file_read`
- **Write**: `edit_file`, `write_file`, `multi_file_edit`
- **Search**: `web_search`, `code_search`, `grep`
- **Reasoning**: `lsp_*` tools (semantic understanding)
- **Execution**: `run_command`, `start_command`, git operations, `file_ops`
- **Other**: everything else

#### 2. Workload Analysis
After minimum 5 tool calls, analyzes the pattern:

- **Execution-heavy** (>70% read/write/search/execution): Suggests cheaper model
- **Reasoning-heavy** (>50% LSP tools): No warning (justifies frontier model)
- **Mixed**: No action

#### 3. Guidance Injection
Non-blocking advisory guidance is injected into tool results when execution-heavy pattern is detected:

```
[FinOps Guidance: Heterogeneous Model Selection]

Your recent actions show an execution-heavy pattern (read/write/search/execution tools dominate).

Cost Optimization Opportunity:
Consider using a cost-effective model tier for routine file operations:
- Frontier models (Claude Opus, GPT-4): Best for complex reasoning, planning, architectural decisions
- Mid-tier models (Claude Sonnet, GPT-4o-mini): Good balance for standard coding tasks
- Small models: Sufficient for repetitive file edits, grep searches, basic text operations

Current pattern appears to be routine execution rather than deep reasoning.
If this is primarily mechanical work, you could reduce token costs by 60-90%
while maintaining quality.

This is guidance only - proceed with the current model if this task requires
frontier-level reasoning capability.
```

### Design Decisions

1. **Zero LLM cost**: Pure pattern matching, no semantic analysis
2. **Non-blocking**: Advisory only, never prevents action
3. **Fires at most once per run**: Prevents repetitive nagging
4. **10-tool lookback window**: Recent behavior matters more
5. **Reasoning-heavy exclusion**: LSP tools justify frontier model cost
6. **Thread-safe**: Uses mutex for concurrent access

### Integration Points

- Initialized in `Agent` struct as `heterogeneousModel` field
- Called in tool execution loop after OOD detection
- Guidance appended to `result.Content` if triggered
- Resets on each new run via `reset()` method

## Testing

3 test cases:

1. **TestHeterogeneousModelGuide**: Verifies execution-heavy pattern triggers guidance
2. **TestHeterogeneousModelReasoningHeavy**: Verifies reasoning-heavy (LSP) doesn't trigger
3. **TestHeterogeneousModelMinActions**: Verifies minimum threshold enforcement

All tests pass.

## Cost Intelligence Synergy

This feature complements existing cost intelligence:
- **Token tracking**: `internal/cost/cost.go` 
- **Budget enforcement**: `internal/agent/cost_budget.go`
- **Success declaration**: `internal/agent/success_declare.go`

Together they provide multi-layered FinOps awareness: tracking actual spend, enforcing limits, and suggesting cost-effective model selection.

## Future Enhancements

Potential improvements:
1. Learn from historical cost-effectiveness patterns
2. Auto-suggest model switching via config
3. Track actual cost savings realized
4. Integrate with provider's model catalog APIs

## References

- Deloitte 2025 AI Agent Trends Report
- Machine Learning Mastery: "FinOps for AI Agents" (2025)
- Plan-and-Execute: arXiv:2308.14342 (2023)
