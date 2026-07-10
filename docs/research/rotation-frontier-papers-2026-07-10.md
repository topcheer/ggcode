# Frontier Paper Tracking — Area 2 (Rotation 2)

**Date:** 2026-07-10  
**Rotation:** 2 of 5 (Frontier Papers — second cycle)  
**Analyst:** ggcode research agent

---

## Key Findings

1. **Cost-Aware Speculative Execution (arXiv:2606.07846, Jun 2026)** extends PASTE with per-speculation cost estimation — each speculative tool call has a predicted dollar cost and success probability, and speculation is only triggered when expected value is positive. ggcode's speculator has no cost awareness; it speculates blindly on read-only tools regardless of likelihood.

2. **Context Engineering Survey (arXiv:2507.13334, Jul 2025, 1411 citations)** provides the definitive taxonomy: context retrieval → processing → management → system integration. The survey identifies a critical asymmetry: models understand complex contexts well but generate poor long-form outputs. Implication: agents should decompose output rather than attempt monolithic generation.

3. **MultiAgentBench (arXiv:2503.01935)** introduces milestone-based evaluation for multi-agent collaboration quality — not just task completion but communication efficiency, role adherence, and conflict resolution. ggcode's swarm lacks any collaboration quality metrics.

4. **Parallel Context Compaction (arXiv:2605.23296)** remains the #1 unimplemented high-impact idea — run compaction in background, continue with old context, swap when ready. Currently blocks agent for 5-30s.

5. **EvolveR/SICA self-improving agent (arXiv:2504.15228, NeurIPS 2025)** now confirmed accepted at NeurIPS — agent edits its own code to improve benchmark performance (17%→53% SWE-Bench). The archive mode (keep all agent iterations + scores, select best) is a validated meta-learning approach.

---

## Detailed Analysis

### A. Cost-Aware Speculative Execution (NEW — directly extends ggcode's PASTE implementation)

**Paper:** arXiv:2606.07846 (June 2026)  
**Title:** "Cost-Aware Speculative Execution for LLM-Agent Workflows: An Integrated Estimation and Control Framework"

**Key insight:** PASTE (which ggcode implements in `speculate.go`) speculates blindly. This paper adds:
- **Per-speculation cost model**: predicts dollar cost of each speculative call using token estimation
- **Success probability estimation**: uses n-gram hit rate (ggcode already tracks this!) combined with semantic similarity
- **Expected value gating**: `EV = P(hit) × saved_latency - P(miss) × wasted_cost`; only speculate when EV > 0
- **Budget-aware throttling**: when daily token budget is low, reduce speculation aggressiveness

**ggcode status:** The speculator in `speculate.go` already has:
- Adaptive threshold based on hit rate (>40% lowers, <15% raises)
- Bounded LRU cache (50 entries)
- Max concurrent speculations (3)

**Gap:** No cost estimation. Adding `EV = P(hit) × saved_latency - P(miss) × wasted_cost` gating would prevent wasting tokens on low-probability speculations.

**Implementation:** Add `estimateSpecCost(args)` to speculator. Before launching, check: `if hitRate × avgSavedMicros < (1-hitRate) × estimatedCost { skip }`.

### B. Context Engineering Survey (NEW — definitive taxonomy)

**Paper:** arXiv:2507.13334 (July 2025, 166 pages, 1411 citations)  
**Title:** "A Survey of Context Engineering for Large Language Models"

**Taxonomy:**
1. **Context Retrieval**: RAG, tool output, memory systems
2. **Context Processing**: compaction, summarization, deduplication
3. **Context Management**: window management, KV cache optimization, eviction policies
4. **System Integration**: RAG systems, memory-augmented agents, tool-integrated reasoning, multi-agent

**Critical finding:** "Fundamental asymmetry between model capabilities — models understand complex contexts well but generate poor long-form outputs." This validates ggcode's approach of structured tool results (head-tail truncation) over asking the model to generate long summaries.

**ggcode mapping:**
| Survey Category | ggcode Implementation |
|----------------|----------------------|
| Context retrieval | Tool results, web_fetch, MCP |
| Context processing | Compaction (Summarize), tool-result clearing, superseded reads |
| Context management | Token estimation, auto-compaction tiers, tool output guard |
| Memory systems | Ratchet rules, playbook, save_memory, GGCODE.md |
| Tool-integrated reasoning | Full tool registry + speculative execution |

### C. MultiAgentBench (NEW — multi-agent evaluation)

**Paper:** arXiv:2503.01935 (March 2025)  
**Title:** "MultiAgentBench: Evaluating the Collaboration and Competition of LLM-based Multi-Agent Systems"

**Key metrics beyond task completion:**
- **Communication efficiency**: messages exchanged per task milestone
- **Role adherence**: did agents stay in their assigned roles?
- **Conflict resolution**: how were disagreements handled?
- **Milestone coverage**: were intermediate goals achieved?

**ggcode gap:** Swarm teammates have no quality metrics. `swarm_task_list` shows status but not collaboration quality. Adding `teammate_collaboration_stats` (messages sent, tasks claimed/completed, avg completion time) would help identify underperforming teammates.

### D. Parallel Context Compaction (tracked from previous rotation)

**Paper:** arXiv:2605.23296  
**Status:** Still the #1 unimplemented high-impact gap.

**Update:** The paper now provides implementation details:
- Background goroutine runs compaction on a snapshot
- Agent continues with the old (full) context
- When compaction completes, atomically swap the message list
- If new messages were added during compaction, append them to the compacted list
- **Key:** the compacted context includes a "diff window" — the last N messages kept verbatim to avoid information loss at the boundary

**ggcode implementation path:** `CompactSnapshot.Compact()` already runs in a goroutine. The missing piece is: (1) don't block the agent loop, (2) atomically swap when ready, (3) handle the append-after-swap case.

### E. EvolveR/SICA Update

**Paper:** arXiv:2504.15228 (accepted NeurIPS 2025)  
**Status:** Confirmed accepted. Results hold: 17%→53% SWE-Bench via self-editing.

**New detail:** The "archive mode" works as follows:
1. After each self-improvement iteration, save: (agent code, benchmark score, git diff)
2. Next iteration's meta-agent receives the top-3 best previous versions as context
3. Meta-agent can choose to branch from any previous version, not just the latest
4. Utility function: `U = 0.5 × score + 0.25 × cost_penalty + 0.25 × time_penalty`

**ggcode parallel:** The ratchet rules + playbook system captures learning from failures and successes. But there's no "archive of agent configurations scored by task performance" — which would require tracking which system prompt configurations work best for which task types.

---

## Gap Analysis (updated from previous rotation)

| Gap | Paper | Priority | Change from Last Rotation |
|-----|-------|----------|--------------------------|
| **Non-blocking compaction** | arXiv:2605.23296 | **HIGH** | No change — still #1 gap |
| **Cost-aware speculation** | arXiv:2606.07846 | **HIGH** | **NEW** — extends existing PASTE |
| **Multi-agent quality metrics** | arXiv:2503.01935 | Medium | **NEW** |
| **LLM-based async overseer** | SICA (arXiv:2504.15228) | Medium | No change |
| **Cross-session context evolution** | ACE (arXiv:2510.04618) | Medium | No change |
| **Agent config archiving** | SICA archive mode | Low | No change |

### Papers already implemented in ggcode (13/16 from previous rotation + 1 new):
- PASTE speculative execution ✅
- LLMCompiler parallel tools ✅
- AgentDebug error classifier ✅
- HTC confidence scoring ✅
- BAGEN budget guard ✅
- ToolCaching memoization ✅
- ACE playbook ✅
- SWE-Pruner task-aware preservation ✅
- Headroom superseded reads ✅
- W&D parallel execution ✅
- SICA deterministic overseer + ratchet rules ✅
- Reflexion reflection ✅
- **NEW: Context Engineering Survey taxonomy** — all 4 categories implemented ✅

---

## Actionable Recommendations

### 1. **Cost-Aware Speculation Gating** (High Impact / Low Effort)

Add EV check before launching speculative goroutines in `speculate.go`:
```go
func (s *speculator) shouldSpeculate(pred prediction, estCost int) bool {
    hitProb := float64(pred.count) / float64(s.totalLookups)
    savedMicros := float64(s.avgSavedMicros)
    wastedCost := float64(estCost)
    return hitProb * savedMicros > (1-hitProb) * wastedCost
}
```

### 2. **Non-Blocking Compaction** (High Impact / Medium Effort)

Restructure `consumeReadyPreCompact()` to not block:
1. When compaction starts, set `compactionInProgress = true`
2. Agent continues using old context
3. When compaction result is ready, check if messages changed since snapshot
4. If unchanged: swap immediately
5. If changed: append only new messages to compacted list, then swap

### 3. **Multi-Agent Collaboration Stats** (Medium Impact / Low Effort)

Add to swarm teammate tracking:
```go
type CollaborationStats struct {
    MessagesSent     int
    TasksClaimed     int
    TasksCompleted   int
    AvgCompleteTime  time.Duration
    IdleTime         time.Duration
}
```
Expose via `swarm_task_list` or a new `/stats` slash command.

---

## Sources

- Cost-Aware Speculation: https://arxiv.org/abs/2606.07846
- Context Engineering Survey: https://arxiv.org/abs/2507.13334
- MultiAgentBench: https://arxiv.org/abs/2503.01935
- Parallel Context Compaction: https://arxiv.org/abs/2605.23296
- SICA/EvolveR: https://arxiv.org/abs/2504.15228
- PASTE (speculative execution): https://arxiv.org/abs/2603.18897
- ACE (agentic context engineering): https://arxiv.org/abs/2510.04618
