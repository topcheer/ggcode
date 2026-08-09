# Research SA-95: Planner-Worker Architecture Gap

## Research Summary (2025-2026 AI Agent Frontier)

### Frontier Concept: Planner-Worker Architecture
Based on research from Zylos.ai (2026), Cursor (GPT-5.2 integration), and Devin production deployments:

**Key Findings:**
- **90% cost reduction** by using capable models (GPT-5.2/Claude Opus) for planning and cheaper models for execution
- **Planner-Worker dominance**: 90% of production systems (Cursor, Claude Code, AWS Strands) use this pattern
- **Task decomposition before execution**: Break complex tasks into ordered checklists before coding begins
- **Hierarchical planning**: Tree-like structures with dependency tracking and parallel execution

### The Architecture

```
┌─────────────────────────────────────┐
│  Planner (Frontier Model)           │
│  - High-level reasoning             │
│  - Task decomposition               │
│  - Strategy creation                │
│  - Quality assurance                │
└──────────────┬──────────────────────┘
│
▼
┌──────────────────────────┐
│  Task Queue              │
└──────────┬───────────────┘
│
┌─────────┴─────────┐
▼                   ▼
┌─────────┐         ┌─────────┐
│ Worker  │   ...   │ Worker  │
│ (Cheap  │         │ (Cheap  │
│ Model)  │         │ Model)  │
└─────────┘         └─────────┘
```

## Gap Analysis for ggcode

### What ggcode HAS (internal/agent/planner.go)
- ✅ Complexity detection (heuristic analysis of user's first message)
- ✅ Suggests using `todo_write` to create structured plans
- ✅ Monitors if agent created a todo list
- ✅ Zero-LLM-cost deterministic approach
- ✅ Reminder system if agent ignores suggestion

### What ggcode LACKS (The Gap)

1. **No Actual Task Decomposition Engine**
   - Current: Only suggests planning, doesn't help structure it
   - Frontier: Planner actively decomposes tasks into sub-goals with dependencies

2. **No Planner-Worker Model Separation**
   - Current: Single agent model for both planning and execution
   - Frontier: Capable model plans once, cheap models execute repetitive tasks
   - Impact: Missing 90% cost optimization opportunity

3. **No Dependency Tracking**
   - Current: Todos are flat list
   - Frontier: DAG with dependencies, parallel execution scheduling

4. **No Hierarchical Context Isolation**
   - Current: Single context window for entire task
   - Frontier: Sub-tasks operate in isolated contexts, preventing 35-minute degradation

5. **No Progress Tracking System**
   - Current: No awareness of what's completed vs what remains
   - Frontier: Explicit progress tracking with ETA and completion percentage

## Priority Assessment

**Implementation Complexity: HIGH**
- Requires new architecture: model selection infrastructure, separate agent processes
- Needs dependency graph management system
- Requires context isolation mechanisms
- Affects multiple components (agent runtime, model provider, context management)

**Business Impact: VERY HIGH**
- 90% cost reduction potential
- Enables multi-hour autonomous tasks (currently degrades after 35 minutes)
- Critical for long-horizon tasks (week-long projects)

**Constraint Conflict:**
- Current task constraints: "不要添加新的 detector，优先考虑代码整合、优化、重构"
- Implementing Planner-Worker requires **new architecture**, not just refactoring
- Would violate "no new detectors" spirit by adding entirely new planning layer

## Recommendation

**Do NOT implement in current research task (sa-95)**

**Rationale:**
1. Architectural scope exceeds "optimization/refactoring" constraint
2. Requires dedicated feature implementation project with proper design
3. Better suited for separate task with focused scope
4. Current planner.go provides useful guidance even in simple form

**Alternative small optimizations:**
- Enhance complexity detection heuristics (tuning thresholds, adding patterns)
- Improve todo suggestion text for better LLM adherence
- Add progress tracking for existing todos (not hierarchical decomposition)
- Refactor duplicate code patterns in agent package (but planner.go functions are too short to benefit)

## Research Sources

1. Zylos.ai - "Long-Running AI Agents and Task Decomposition 2026"
2. Deloitte Insights - "Agentic AI Strategy 2026"
3. Cursor AI - GPT-5.2 integration documentation
4. Cognition Labs - "Devin's 2025 Performance Review" (18 months production data)
5. ArXiv:2508.11957 - "A Comprehensive Review of AI Agents"

## Backlog Item (Unprioritized)

```
Title: Implement Planner-Worker Architecture
Type: Major Feature
Priority: High (based on cost reduction and long-horizon capability)
Complexity: Very High
Dependencies: Model provider refactoring, context management overhaul
Estimated Effort: 3-5 weeks
Blocked By: Task constraint (sa-95 scope is optimization/refactoring only)
```

---

**Research Completed:** 2025-XX-XX (sa-95)
**Conclusion:** Gap identified and documented, but not implemented due to architectural scope constraint.
