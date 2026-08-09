# SA-103: Cross-Agent Skill Distillation Gap Analysis

## Research Summary

**Concept**: Multi-Agent Skill Distillation
**Source**: arXiv 2604.01608 "From Multi-Agent to Single-Agent: When Is Skill Distillation Beneficial?" (2025-2026)

### Key Insight from Research

Multi-agent systems (MAS) tackle complex tasks by distributing expertise, but this comes with:
- **Heavy coordination overhead**
- **Context fragmentation**
- **Brittle phase ordering**

Distilling a MAS into a single-agent skill can bypass these costs. The paper provides a principled framework for **when** and **what** to distill.

## ggcode's Current State

### Existing Multi-Agent Capabilities

ggcode has robust multi-agent support:

1. **Swarm System** (`internal/swarm/manager.go`):
   - Persistent teammate agents
   - Task board with assignments
   - Inbox-based coordination

2. **A2A Protocol** (`internal/a2a/`):
   - Agent-to-Agent communication
   - Cross-project delegation
   - JSON-RPC 2.0 based

3. **Delegation Orchestration** (`internal/agent/delegation_orchestration.go`):
   - Orphaned delegation detection
   - Serial delegation anti-pattern warnings
   - Over-delegation guard (60% threshold)

4. **LanChat** (`internal/lanchat/`):
   - Real-time agent collaboration
   - Broadcast/DM messaging

## The Gap

**Missing**: Skill Distillation Mechanism

ggcode supports multi-agent coordination but lacks:
1. **Pattern Recognition**: No mechanism to identify when multi-agent solutions could be consolidated into single-agent skills
2. **Knowledge Extraction**: No way to extract successful multi-agent workflows as reusable patterns
3. **Distillation Guidance**: No heuristics to suggest when coordination overhead outweighs benefits
4. **Skill Consolidation**: No mechanism to package successful agent collaboration into more efficient forms

### Concrete Examples

**Scenario A - Over-delegation**:
```
Agent A → delegate to Agent B → delegate to Agent C → delegate to Agent D
```
Current: `delegation_orchestration.go` warns about >60% delegation ratio
Missing: Suggests consolidating B/C/D work into a single skill

**Scenario B - Repeated Multi-Agent Pattern**:
```
Task 1: Agent A → Agent B → Agent C (success)
Task 2: Agent A → Agent B → Agent C (success)
Task 3: Agent A → Agent B → Agent C (success)
```
Current: No pattern tracking
Missing: Detect this is a stable pattern and suggest distilling to single agent

**Scenario C - Context Fragmentation**:
```
Agent A has context X, Agent B has context Y
Collaboration requires X↔Y handoff repeatedly
```
Current: A2A handles handoff but doesn't measure cost
Missing: Quantify handoff cost and suggest consolidation

## Implementation Approach

### Priority: LOW (Research Gap, Not Critical Bug)

**Rationale**:
- Skill distillation is an advanced optimization, not a correctness issue
- Requires careful heuristics to avoid premature consolidation
- Needs ML/statistical analysis of agent behavior patterns
- Risk of breaking working multi-agent workflows

### Recommended Direction: Code Consolidation First

Given the constraint "don't add new detectors, prioritize code integration/optimization":

**Option 1: Enhance Delegation Orchestration** (Medium effort)
- Extend `delegation_orchestration.go` to track successful delegation chains
- Add heuristics to suggest when chains could be flattened
- Reuse existing orphan detection infrastructure

**Option 2: Document Pattern Library** (Low effort)
- Create a guide on when to use multi-agent vs single-agent
- Document anti-patterns (over-delegation, context fragmentation)
- Provide refactoring examples

**Option 3: No Action** (Recommended for now)
- This is a research gap, not a production issue
- Current multi-agent system works well for ggcode's use cases
- Wait for more concrete user feedback on coordination overhead
- Focus on higher-priority optimizations

## Conclusion

**Gap Identified**: Yes, ggcode lacks explicit skill distillation from multi-agent to single-agent forms.

**Priority**: LOW - This is an optimization opportunity, not a functional gap.

**Recommendation**: Document the concept in design notes, but do not implement. Current multi-agent coordination is sufficient for ggcode's needs. Skill distillation becomes valuable when:
- Coordination overhead is measurable and problematic
- Repeated multi-agent patterns are common
- There's demand for consolidating complex workflows into efficient single-agent skills

**When to Revisit**:
- Users report "too many agents talking to each other"
- Performance metrics show high inter-agent message latency
- A pattern emerges of similar multi-agent workflows being repeated

## References

- He et al. (2025). "Training One Model to Master Cross-Level Agentic Actions via Reinforcement Learning" (CrossAgent)
- Xu et al. (2025). "From Multi-Agent to Single-Agent: When Is Skill Distillation Beneficial?" arXiv:2604.01608
- Jeong (2025). "A Study on the MCP x A2A Framework for Enhancing Interoperability of LLM-based Autonomous Agents"
