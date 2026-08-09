# sa-99: Tool Schema Lazy Loading / Classifier-First Tool Selection

## Research Summary

Researched 2025-2026 AI Agent production patterns for **Tool Schema Lazy Loading / Classifier-First Tool Selection**, a technique to reduce tool schema token overhead by only sending relevant tools to the LLM.

## Research Sources

1. **KVFlow: Efficient Prefix Caching for Accelerating LLM-Based Multi-Agent Workflows** (arXiv:2507.07400, Jul 2025)
   - Focuses on KV cache management for agentic workflows
   - Workflow-aware eviction policy vs naive LRU
   - Prefetching for next-step agents

2. **Tool-Calling Schema Design for LLM Agents** (AppScale Blog, 2026 Production Pattern)
   - Lazy tool loading via classifier-first cuts schema-tax tokens by 80% on 50+ tool catalogs
   - Combined with prompt cache, savings compound to 95% vs naive baseline
   - Versioned tool registry, per-tenant allow-lists, capability tokens

3. **Tool Call Reliability Patterns for Production AI Agents in 2026** (AgentMarketCap, Apr 2026)
   - Similar lazy loading patterns
   - Tool call reliability patterns

## Gap Analysis

### Expected Gap
- Agents often send ALL tool schemas to the LLM on every turn
- This wastes tokens when most tasks only need a small subset of available tools
- Classifier-first approach: lightweight intent classifier → filter relevant tools → send only those

### Actual State in ggcode
**NO GAP FOUND** — ggcode already implements a sophisticated version of this concept:

**File**: `internal/tool/relevance.go` (347 lines)

**Key Features**:
1. **Activation Threshold**: Only filters when tool count > 30 (`minToolsToActivate`)
2. **Built-in Tools**: Always kept (core functionality, well-understood by LLM)
3. **MCP/Plugin Tools**: Scored via keyword relevance matching against conversation context
   - Uses lightweight tokenization (no LLM calls)
   - Tools with names containing "__" (MCP plugins) are scored
   - Minimum relevance threshold: 0.05 (`mcpScoreThreshold`)
4. **Per-Server Limits**: Maximum 15 MCP tools per server (`maxMCPToolsPerServer`)
5. **Context-Pressure-Aware Budgeting**: 
   - Trims tool descriptions more aggressively when context utilization is high
   - `FilterWithPressure()` takes `pressure` parameter (0.0-1.0)
   - Reduces description length when context window is filling up
6. **Zero LLM Cost**: All scoring uses simple keyword matching and tokenization

**Integration**: Called from `internal/agent/agent.go:2262` before `streamChatResponse()`:
```go
activeToolDefs := a.toolFilter.FilterWithPressure(toolDefs, tool.ExtractContextFromMessages(msgs, 6), ctxPressure)
resp, textBuf, toolCalls, truncated, err := a.streamChatResponse(ctx, a.ensureMessagesSendable(msgs), activeToolDefs, onEvent)
```

## Comparison with Research Patterns

| Aspect | 2026 Production Pattern | ggcode Implementation |
|--------|-------------------------|----------------------|
| Lazy Loading | Classifier-first, 80% schema reduction | Activation threshold (30) + relevance scoring |
| Tool Selection | Intent-based classifier | Keyword relevance scoring (zero LLM cost) |
| Token Savings | 80-95% reduction vs baseline | Pressure-aware description trimming |
| Fallback Strategy | All tools if classifier uncertain | All tools if < 30 total tools |
| LLM Overhead | Small classifier call | **Zero** LLM calls for classification |

## Assessment

ggcode's `RelevanceFilter` is **more sophisticated** than the basic pattern described in the research literature:

### Advantages over Research Patterns
1. **No LLM Overhead**: Research patterns use lightweight classifiers (still LLM calls). ggcode uses pure keyword matching — zero inference cost.
2. **Context-Pressure-Aware**: ggcode dynamically adjusts description trimming based on actual context utilization, not just static filtering.
3. **Built-in Tool Safety**: Always keeps built-in tools, avoiding the risk of filtering out core functionality.
4. **Per-Server Limits**: Prevents a single MCP server from dominating the tool list.
5. **Adaptive Activation**: Only filters when needed (threshold 30), avoiding unnecessary overhead for small tool sets.

### Potential Optimizations (Low Priority)
The implementation is solid. Possible future enhancements (not gaps, just optimizations):
1. **Semantic Similarity**: Consider embeddings for tool description matching (higher cost, better accuracy)
2. **Usage Statistics**: Track historical tool usage patterns to improve relevance scoring
3. **User-Defined Tool Categories**: Allow users to tag tools with intent categories for better classification

However, these are enhancements, not gaps. The current implementation already solves the core problem identified in the research.

## Conclusion

**No implementation needed.** The concept researched is already implemented in ggcode with additional optimizations not mentioned in the 2026 production pattern literature.

### Key Differentiator
ggcode's approach is **more practical for production use** than the research patterns because:
- Zero LLM overhead for classification (pure Go, deterministic, no latency)
- Context-pressure-aware budgeting (adapts to actual window usage)
- Conservative safety defaults (built-in tools always kept)

### Recommendation
Document this capability in user-facing docs so users are aware of the optimization. Consider adding a debug log entry when filtering activates to make the behavior transparent.

**Status**: ✅ **Already Implemented** — No gap found
