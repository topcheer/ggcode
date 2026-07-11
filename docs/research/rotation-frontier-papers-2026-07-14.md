# Frontier Paper Tracking — Rotation 3 (July 2026)

**Date:** 2026-07-14
**Rotation Position:** Area 2 of 5 (Frontier Paper Tracking)
**Previous Rotation:** 2026-07-10 (same area)

## Key Findings

1. **Self-correction has a mathematical stability threshold** (arXiv:2604.22273): Iterative self-correction only helps when `ECR/EIR > Acc/(1-Acc)`. A sharp EIR boundary (~0.5%) separates beneficial from harmful correction. Only 3 of 7 frontier models benefit from self-correction; the rest degrade. This has direct implications for ggcode's error-streak and circuit breaker systems.

2. **Tool Inertia Graphs formalize what ggcode's speculator partially does** (AAAI 2026, arXiv:2511.14650): Tool calls follow predictable sequential patterns (e.g., `go_to` → `look_around` 88.7% of the time). AutoTool achieves 1.6× token-in speedup by bypassing LLM inference for high-confidence tool predictions. ggcode's speculator uses bigram patterns but misses parameter-level data flow modeling.

3. **Agent self-correction has 3 distinct failure modes** that standard benchmarks miss: false convergence (agent thinks it fixed the problem but only suppressed the symptom), correction-induced regression (fix for error A introduces error B), and context collapse on long repair chains (after 8-10 corrections, models lose original task context).

4. **Agent memory requires a write-manage-read loop, not just write-read** (arXiv:2603.07670): Production memory systems need filtering, canonicalization, deduplication, staleness detection, contradiction resolution, and periodic consolidation sweeps. ggcode's ratchet rules and playbook capture procedural/strategy memory but lack temporal versioning and contradiction detection.

5. **Cross-session memory is the #1 open problem** in agent memory research (Mem0 2026 report): LoCoMo, LongMemEval, and BEAM benchmarks now standardize evaluation. Token-efficient retrieval (~7K tokens) dramatically outperforms full-context (~26K tokens). Identity resolution across devices/sessions remains unsolved.

## Detailed Analysis

### 1. Self-Correction Stability Theory (arXiv:2604.22273)

**Paper:** "Self-Correction as Feedback Control: Error Dynamics, Stability Thresholds, and Prompt Interventions in LLMs" (Liu & Meng, 2026)

This paper recasts iterative self-correction as a closed-loop feedback control problem where the same LLM is both controller and plant. The key innovation is a two-state Markov model over {Correct, Incorrect} states parameterized by:
- **EIR (Error Introduction Rate):** Probability of introducing a new error during correction
- **ECR (Error Correction Rate):** Probability of fixing an existing error

**Stability threshold:** `ECR/EIR > Acc/(1-Acc)` — iterate only when this holds.
**Empirical boundary:** EIR ≤ 0.5% cleanly separates beneficial from harmful self-correction.

**Results across 7 models (GSM8K, 4 iterations):**
| Model | EIR | Self-Correction Effect |
|-------|-----|----------------------|
| o3-mini | 0% | +3.4 pp |
| Claude Opus 4.6 | ~0.2% | +0.6 pp |
| o4-mini | ~0.2% | ±0 pp |
| GPT-5 | ~2% | -6.2 pp (degraded) |
| GPT-4o-mini | 2% → 0% (with verify-first) | -6.2 pp → +0.2 pp |

**Key insight:** A "verify-first" prompt intervention reduces EIR from 2% to 0%, converting harmful loops into stable ones. This is lightweight controller design — prompt-level EIR suppression.

**Relevance to ggcode:**
ggcode has progressive error-streak detection (4/7/10 errors) and a hard circuit breaker (3 consecutive failures). But neither measures EIR/ECR. The paper suggests a more principled approach: estimate EIR per model and disable self-correction loops when EIR is above threshold.

### 2. AutoTool: Tool Inertia Graph (AAAI 2026, arXiv:2511.14650)

**Paper:** "AutoTool: Efficient Tool Selection for Large Language Model Agents"

AutoTool identifies "tool usage inertia" — tool invocations follow predictable sequential patterns validated by k-th order Markov chain analysis (conditional entropy drops from 3.50 bits at 0th order to 1.93 bits at 2nd order).

**Architecture:**
1. **Tool Inertia Graph (TIG):** Hierarchical graph with Tool Nodes (containing Parameter sub-graphs) and two edge types: Tool Sequence Edges (sequential dependencies) and Parameter Dependency Edges (inter-tool data flow)
2. **Inertia Sensing (CIPS Score):** `(1-α)·freq_score + α·context_score` — only bypasses LLM when score > θ_inertial
3. **Hierarchical Parameter Filling:** Dependency backtracking → environment state matching → heuristic filling

**Results:** 1.6× token-in speedup, 2.87× token-out speedup on AlfWorld with maintained task completion rates.

**Safety constraints:** Inertia calls capped at 30%, consecutive inertia calls prohibited, fault-tolerance recovery on consecutive failures.

**Relevance to ggcode:**
ggcode's speculator (`speculate.go`) already implements bigram pattern-based prediction with argument-linked patterns (edit→read path prediction). But AutoTool adds:
- **Parameter Dependency Edges:** Modeling data flow between tools (e.g., `read_file` output → `edit_file` input), enabling automatic parameter filling
- **Confidence-scored bypass:** CIPS score with dynamic α mixing frequency and semantic similarity, more sophisticated than ggcode's count-based threshold (minCount=2)
- **Online reinforcement:** Edge weights updated via positive/negative feedback from success/failure

### 3. Agent Self-Correction Failure Modes (AgentMarketCap, 2026)

Three failure modes that standard benchmarks miss:

1. **False convergence:** Agent believes it fixed the problem (high confidence signals) but only suppressed the symptom. A calibration failure.
2. **Correction-induced regression:** Fix for error A introduces error B. Correction loop oscillates indefinitely without holistic diff analysis.
3. **Context collapse on long repair chains:** After 8-10 tool calls or correction rounds, models lose context about the original task specification. Corrections become local and disconnected from the broader goal.

**Practical evaluation framework (4 tiers):**
- Tier 1: Function-level repair (single round) — minimum bar, ~70% repair vs generation score
- Tier 2: Multi-file repair with context — cross-file dependency understanding
- Tier 3: Extended correction chains (3-5 attempts) — strategic behavior vs monotonic degradation
- Tier 4: Calibration under uncertainty — confidence correlation with accuracy

**Relevance to ggcode:**
ggcode's confidence scorer (`confidence.go`) partially addresses Tier 4. The repetition tracker catches near-miss edit loops. But ggcode lacks:
- **Regression detection:** No mechanism to detect that a fix introduced a new error elsewhere
- **Holistic diff analysis:** No comparison of pre/post state across multiple files after corrections
- **Context anchoring:** No mechanism to prevent context collapse during long repair chains (constraint pinning during compaction helps, but doesn't address within-turn degradation)

### 4. Memory for Autonomous LLM Agents (arXiv:2603.07670)

**Paper:** "Memory for Autonomous LLM Agents: Mechanisms, Evaluation, and Emerging Frontiers" (Du, 2026)

Formalizes agent memory as a **write-manage-read loop** with a 3D taxonomy:
- **Temporal scope:** Short-term (context window) → Working (session) → Long-term (cross-session) → Episodic (specific events) → Semantic (generalized knowledge) → Procedural (how-to skills)
- **Representational substrate:** Text embeddings → Structured graphs → Knowledge bases → Neural weights
- **Control policy:** LLM-managed → Rule-based → Learned → Hybrid

**Five mechanism families:**
1. Context-resident compression (ggcode has this: tool-result clearing, superseded reads)
2. Retrieval-augmented stores (ggcode lacks: no vector DB, no semantic retrieval)
3. Reflective self-improvement (ggcode has this: ratchet rules, playbook)
4. Hierarchical virtual context (ggcode partially: precompact summarization)
5. Policy-learned management (ggcode lacks: no learned memory policies)

**Write-path engineering essentials:**
- Filtering (reject low-signal records)
- Canonicalization (normalize dates, names, quantities)
- Deduplication (merge overlapping entries)
- Priority scoring (rank by task relevance and novelty)
- Metadata tagging (timestamp, source, task label, confidence)

**Read-path optimizations:**
- Two-stage retrieval (fast BM25 → slow cross-encoder reranker)
- Retrieval-or-not gating (skip retrieval for straightforward requests)
- Token budgeting (dynamically allocate between memory and task)
- Cache layers for high-frequency records

**Critical gap — staleness and contradictions:**
Production systems need temporal versioning (prefer newest), source attribution (user statement > agent inference), contradiction detection, and periodic consolidation sweeps. Without these, memory accumulates stale and conflicting entries.

**Relevance to ggcode:**
ggcode's memory systems:
- ✅ Ratchet rules (procedural memory — error patterns → prevention rules)
- ✅ Playbook (procedural memory — strategy patterns → hints)
- ✅ save_memory (manual semantic memory)
- ✅ Reflection (episodic — run-level self-assessment)
- ❌ No temporal versioning (ratchet rules can become stale)
- ❌ No contradiction detection (conflicting rules can coexist)
- ❌ No periodic consolidation sweeps
- ❌ No retrieval gating (all ratchet rules injected at run start)
- ❌ No cross-session episodic memory (each session starts fresh)

### 5. CORAL: Self-Evolving Multi-Agent Systems

**Paper:** "CORAL: Towards Autonomous Multi-Agent Evolution for Open-Ended Discovery" (2026)

CORAL introduces long-running multi-agent systems that self-evolve via:
- **Shared persistent memory:** Agents contribute to and read from a common memory store
- **Asynchronous execution:** Agents operate independently with heartbeat-based coordination
- **Heartbeat-based interventions:** Periodic check-ins that trigger strategy adjustments

**Results:** 3-10× higher improvement rates than fixed evolutionary-search baselines on 10 math/algorithmic/systems tasks.

**Relevance to ggcode:**
ggcode's swarm teams have shared task boards and message passing, but agents don't evolve their strategies based on collective experience. The playbook could serve as shared persistent memory, but it's per-workspace not per-team.

### 6. Additional Notable Papers

**ROMA (Recursive Open Meta-Agent):** Breaks large tasks into subtask trees that run in parallel across agents to handle long-horizon workflows without exceeding context windows. Relevant to ggcode's sub-agent delegation.

**Mem2ActBench:** Benchmarks whether agents can proactively use long-term memory to execute tool-based actions (not just passively retrieve facts). Tests the transition from "knowing" to "doing."

**RealMem:** Cross-session dialogue benchmark with 2,000+ dialogues across 11 scenarios. Evaluates how well agents track evolving goals and dynamic context dependencies.

**Agent Drift (2026):** Introduces a composite metric framework for quantifying semantic, coordination, and behavioral degradation in multi-agent LLM systems over extended interactions.

**Lost in the Noise (2026):** Benchmarks model robustness against contextual noise types including random documents, irrelevant histories, and hard negative distractors across 11 RAG, reasoning, alignment, and tool-use tasks.

## Gap Analysis

### Already Implemented in ggcode (14/18 concepts tracked)

| Concept | ggcode Implementation | Source Paper |
|---------|----------------------|--------------|
| Speculative tool execution | speculate.go (bigram patterns) | PASTE, AutoTool |
| Tool result memoization | memoize.go | ToolCaching |
| Progressive error streak | loop_detect.go (4/7/10) | SICA |
| Circuit breaker | circuit_breaker.go | Cordum |
| Confidence scoring | confidence.go | HTC |
| Error classification | error_classifier.go | AgentDebug |
| Budget guard | budget_guard.go | BAGEN |
| Context compaction | agent_precompact.go | ACE, ACON |
| Superseded reads | manager.go | Headroom |
| Tool output guard | tool_output_guard.go | Context Engineering |
| Constraint pinning | manager.go | Governance Decay |
| Ratchet rules (procedural memory) | ratchet.go | SICA |
| Strategy playbook | playbook.go | ACE |
| Parallel tool execution | parallel_tools.go | LLMCompiler, W&D |

### Gaps Identified (ranked by impact/effort)

1. **EIR-based self-correction gating** (High/Medium) — No EIR/ECR measurement or stability-based gating
2. **Parameter dependency modeling** (Medium/Medium) — Speculator doesn't model data flow between tools
3. **Regression detection** (Medium/Medium) — No mechanism to detect correction-induced new errors
4. **Memory staleness detection** (Medium/Low) — Ratchet rules can become stale with no expiration
5. **Contradiction detection in rules** (Medium/Low) — Conflicting ratchet rules can coexist
6. **Cross-session episodic memory** (High/High) — No persistent memory across sessions beyond ratchet/playbook
7. **Retrieval gating** (Medium/Low) — All ratchet rules injected at run start regardless of relevance
8. **Context anchoring for long repair chains** (Medium/Medium) — No mechanism to prevent context collapse during extended correction sequences within a single turn

## Actionable Recommendations

### 1. EIR-Based Self-Correction Gating (High Impact / Medium Effort)
**Source:** arXiv:2604.22273
**Concept:** Track Error Introduction Rate per model and disable self-correction when EIR > 0.5%.

**Implementation:**
- Add EIR/ECR tracking to the agent loop: when an error-streak correction round introduces a NEW error type (not the original), increment EIR counter. When it fixes the original error, increment ECR counter.
- After 10+ correction rounds, compute `ECR/EIR` ratio. If below `Acc/(1-Acc)` threshold, inject guidance: "Your correction attempts are introducing more errors than they fix. Consider stepping back and re-reading the original requirements."
- This is a lightweight addition to `loop_detect.go` — no new files needed.

**Next step:** Add `eirCount` and `ecrCount` fields to loop detector state. Classify each correction round outcome as "fixed", "introduced new", or "no change".

### 2. Parameter Dependency Edges in Speculator (Medium Impact / Medium Effort)
**Source:** arXiv:2511.14650 (AutoTool)
**Concept:** Model parameter data flow between tools beyond just path prediction.

**Implementation:**
- Extend speculate.go's argument prediction beyond `edit_file → read_file (same path)` to include:
  - `read_file → grep (path as include_pattern)`
  - `search_files → read_file (first match path)`
  - `run_command → read_file (error file path from stderr)`
- Add a `paramDeps` map: `map[toolPair]paramMapping` where paramMapping describes how to derive the next tool's argument from the previous tool's output.

**Next step:** Audit the top 10 most common tool sequences in ggcode (from speculator stats) and identify parameter dependencies. Add mappings for the top 5.

### 3. Regression Detection (Medium Impact / Medium Effort)
**Source:** Agent self-correction failure modes literature
**Concept:** After a correction round, check if the fix introduced errors in files OTHER than the one being fixed.

**Implementation:**
- In the agent loop, after an `edit_file`/`write_file`/`multi_edit_file` that follows an error (correction context), record the set of files that had errors BEFORE the fix.
- After the fix, run a lightweight check: `git diff --name-only` to see what changed.
- If new files changed that weren't in the original error scope, inject: "Your correction modified files outside the original error scope. Verify these changes don't introduce regressions."

**Next step:** Add a `correctionScope` tracker in `repetition_tracker.go` that records file paths from the original error context.

### 4. Ratchet Rule TTL & Staleness (Medium Impact / Low Effort)
**Source:** arXiv:2603.07670 (Memory for Autonomous LLM Agents)
**Concept:** Add temporal versioning and expiration to ratchet rules.

**Implementation:**
- Add `CreatedAt time.Time` and `LastHit time.Time` to ratchet rules (if not already present).
- Add a `StaleAfter` duration (default 30 days). Rules not hit in 30 days get deprioritized.
- Add a consolidation sweep: periodically merge rules with similar MatchPatterns and merge FixHints.
- Add contradiction detection: if two rules have overlapping MatchPatterns but conflicting FixHints, flag for review.

**Next step:** Check ratchet.go for existing timestamp fields. Add `LastHit` tracking and a `CleanStale()` method.

### 5. Selective Ratchet Rule Injection (Medium Impact / Low Effort)
**Source:** arXiv:2603.07670 (read-path optimizations)
**Concept:** Don't inject all ratchet rules at run start — inject only those relevant to the current task.

**Implementation:**
- Currently `TopRulesForPrompt(N)` injects the top N rules by hit count globally.
- Change to: classify the user prompt (bugfix/feature/refactor/review/test) and inject only rules matching that classification.
- This reduces context bloat and improves rule relevance.

**Next step:** Use playbook's task type classifier to filter ratchet rules for injection.

## Sources

- Self-Correction as Feedback Control: https://arxiv.org/abs/2604.22273
- AutoTool (AAAI 2026): https://arxiv.org/abs/2511.14650
- Agent Self-Correction Benchmarks 2026: https://agentmarketcap.ai/blog/2026/04/10/agent-self-correction-benchmarks-2026
- Memory for Autonomous LLM Agents: https://arxiv.org/abs/2603.07670
- AI Agent Memory 2026 (Mem0): https://mem0.ai/blog/state-of-ai-agent-memory-2026
- CORAL: https://github.com/VoltAgent/awesome-ai-agent-papers (Multi-Agent section)
- ROMA: https://github.com/VoltAgent/awesome-ai-agent-papers (Multi-Agent section)
- Mem2ActBench: https://github.com/VoltAgent/awesome-ai-agent-papers (Eval & Observability section)
- RealMem: https://github.com/VoltAgent/awesome-ai-agent-papers (Eval & Observability section)
- Agent Drift: https://github.com/VoltAgent/awesome-ai-agent-papers (Eval & Observability section)
- Lost in the Noise: https://github.com/VoltAgent/awesome-ai-agent-papers (Eval & Observability section)
- VoltAgent Curated 2026 Papers: https://github.com/VoltAgent/awesome-ai-agent-papers (367 papers)
- SWE-Bench Leaderboard May 2026: https://www.marc0.dev/en/leaderboard
- Awesome LLM Agent Orchestration: https://github.com/CuiZHIQ/Awesome-LLM-Agent-Orchestration
