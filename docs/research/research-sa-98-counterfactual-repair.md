## SA-98: CausalFlow / Counterfactual Repair Research

**Date**: 2026-01-15
**Paper**: CausalFlow: Causal Attribution and Counterfactual Repair for LLM Agent Failures (arXiv:2605.25338, May 2026)

### Research Concept

CausalFlow introduces two components:
1. **Causal Attribution**: Modeling execution traces as sequential chains and computing Causal Responsibility Scores (CRS) via counterfactual intervention to identify failure-inducing steps
2. **Counterfactual Repair**: Generating minimally edited repairs that flip the final outcome to success, producing validated contrastive pairs (wrong step, corrected step)

### Gap Analysis

#### What ggcode Has
- **Causal Attribution**: Fully implemented in `internal/agent/causal_attribution.go` (commit 063b0932)
  - Maintains chronological log of edit steps
  - Computes CRS based on file matches, package proximity, and recency
  - Threshold: CRS >= 25 triggers guidance
  - Zero LLM cost, deterministic

#### What ggcode Lacks
- **Counterfactual Repair**: Automatic generation of minimal edit suggestions from failure traces
  - Requires LLM calls to parse error messages and generate fix suggestions
  - Produces contrastive pairs (wrong → corrected) for training
  - Test-time repair: recover from failures with minimal behavioral drift

### Why Not Implemented

1. **Architecture Conflict**: ggcode prefers deterministic, zero-LLM-cost detectors. Counterfactual repair requires LLM invocation per failure.

2. **Sufficient Alternative**: Current causal attribution already identifies the problematic step and injects guidance. The agent can retry with this information.

3. **Cost/Benefit**: Adding LLM-powered repair would increase latency and cost for every failure, with marginal improvement over the existing attribution + retry pattern.

4. **Training Focus**: CausalFlow's primary benefit is generating training data (contrastive pairs). ggcode is a production agent, not a training framework.

### Conclusion

**No actionable gap found**. Counterfactual Repair would require an architectural shift toward LLM-powered suggestion generation, which conflicts with ggcode's design philosophy. The current implementation (causal attribution + guidance injection + retry) is aligned with ggcode's goals and does not need extension.

### Alternatives Considered

- **Rule-based repair**: Generate simple regex-based fixes (e.g., add missing imports). Rejected: too brittle, would create new detectors.
- **Prompt-based repair**: Inject repair instructions into next turn. Rejected: agent already receives causal attribution guidance.
- **Refactor existing attribution**: Could integrate suggestion prompts. Low priority: attribution is already effective.

### Related Implemented Features

- `causal_attribution.go`: Causal Responsibility Scores (CRS) to identify failure-inducing steps
- `counterfactual_dep.go`: Detects false dependency assumptions in parallel tool calls
- `correction_feedback.go`: Surfaces user undo signals as explicit guidance
