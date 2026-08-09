# SA-105: Meta-Cognitive Monitoring Gap Analysis

**Research Date**: 2025-06-28
**Research Focus**: Meta-cognitive monitoring and confidence calibration in AI agents

## Research Sources

1. **Agentic Confidence Calibration** (Zhang et al., arXiv:2601.15778, Jan 2026)
   - **Holistic Trajectory Calibration (HTC)**: Process-level features from macro dynamics to micro stability
   - **Core Problem**: "Overconfidence in failure" - agents continue confidently even when early decisions have doomed the trajectory
   - **Key Insight**: 10% miscalibration per step compounds to ~35% error over 4 sequential steps

2. **AI Awareness** (Li et al., arXiv:2504.20084, Sept 2025)
   - **Meta-cognition**: Self-monitoring, self-reflection, cognitive control
   - Agents need intrinsic awareness of their own cognitive processes

## Gap Analysis

### What ggcode Already Implements (Well-Covered)

#### 1. Confidence Scoring (HTC-Inspired)
**File**: `internal/agent/confidence.go`

ggcode implements a **deterministic HTC-inspired confidence scorer** with:

**Macro Dynamics (Trajectory-Level)**:
- Tool diversity: using many different tools suggests exploration
- File diversity: touching many files suggests progress
- Trajectory length: diminishing returns after many iterations

**Micro Stability (Per-Step)**:
- Overall success rate: fraction of tool calls that succeed
- Edit success rate: fraction of file edits that succeed
- Recent momentum: last-5-call success rate vs overall
- Error concentration: clustered errors are worse than spread-out ones

**Score**: 0-100, fires warning when <30 early in trajectory

#### 2. Trajectory Health Synthesis
**File**: `internal/agent/trajectory_health.go`

**5 Orthogonal Dimensions**:
1. Edit efficiency: edits/iteration ratio (high retries = risk)
2. Error accumulation: tool errors per iteration
3. Tool success rate: failed tool calls / total tool calls
4. Exploration stagnation: read-only tools vs productive tools
5. Assumption density: assumption language frequency

**Design**: Composite score 0-10, fires when 5+ points (multiple sub-threshold signals accumulating)

This directly implements the "multiple orthogonal signals combined" pattern from the research.

#### 3. Evidence-Induced Overconfidence Detection
**File**: `internal/agent/evidence_overconfidence.go`

**Patterns Detected**:
- Evidence cascade: code edited after evidence tools (web_search, grep, read) without verification
- Definitive claims derived from evidence: "the docs say", "based on my search", "this confirms"

**Research Alignment**: Implements the "evidence tools induce false certainty" finding from Agentic Confidence Calibration.

#### 4. Unverified Confidence Detection
**File**: `internal/agent/unverified_confidence.go`

**Patterns Detected**:
- Overconfident completion claims: "definitely works", "fix is complete", "guaranteed to work"
- Verification gap: claims without build/test/lint

**Research Alignment**: Implements EpiCaR (arXiv:2601.06786) finding: "agents lose calibration, expressing high confidence even when they haven't verified their work."

### Actual Gap Identified

**Learning-Based Calibration vs. Deterministic Heuristics**

The Agentic Confidence Calibration paper proposes:
> "Powered by a simple, interpretable model, HTC consistently surpasses strong baselines... achieves generalization through a General Agent Calibrator (GAC)"

**ggcode's Current Approach**: Pure deterministic heuristics (regex, thresholds, sliding windows)
- **Advantage**: Zero LLM cost, interpretable, always works
- **Limitation**: Cannot learn from historical trajectory data; thresholds are manually tuned

**Research Suggestion**: Train a lightweight model on trajectory features to predict failure/outcomes
- **Benefit**: Better generalization, adaptive to different domains
- **Challenge**: Requires collecting and labeling trajectory data

### Implementation Priority Assessment

**Priority: LOW - Defer to Future Research**

**Rationale**:
1. **Deterministic approach is working**: ggcode's existing detectors already catch most meta-cognitive failures
2. **Learning requires data**: Would need extensive trajectory collection and labeling before training
3. **Zero LLM cost advantage**: Current approach doesn't require model inference
4. **Interpretability trade-off**: Learned models are black boxes; current code is fully auditable

**Alternative Direction**: Enhanced deterministic features

Rather than full learning-based calibration, consider:
- Adding more trajectory-level features (e.g., code complexity changes, test coverage delta)
- Cross-validation between detectors (e.g., if 3+ detectors fire, escalate to user)
- Domain-specific calibration rules (e.g., different thresholds for web vs. systems programming)

## Conclusion

**No immediate implementation gap found**. ggcode's meta-cognitive monitoring is already sophisticated and aligns well with 2025-2026 research directions:

- ✅ Holistic trajectory confidence scoring (HTC-inspired)
- ✅ Multi-signal synthesis (5 orthogonal dimensions)
- ✅ Evidence→certainty calibration (tools induce false certainty)
- ✅ Verification gap detection (overconfidence without testing)

**Recommended Action**: Document current capabilities as "meta-cognitive monitoring layer" and defer learning-based calibration until trajectory data collection infrastructure is in place.

## Related Existing Research

- **SA-99**: Tool Schema Lazy Loading (different focus - tool discovery)
- **SA-100**: Selective Memory Sharing (different focus - memory architecture)
- **SA-101**: Tool Meta-Learning (different focus - tool selection)
- **trajectory-health**: Implemented
- **evidence-overconfidence**: Implemented
- **unverified-confidence**: Implemented
- **confidence-scorer**: Implemented

## References

1. Zhang, J., Xiong, C., & Wu, C.-S. (2026). Agentic Confidence Calibration. arXiv:2601.15778.
2. Li, X., Shi, H., Xu, W., & Xu, R. (2025). AI Awareness. arXiv:2504.20084.
3. Zhu, Y., et al. (2025). Scaling Test-time Compute for LLM Agents. arXiv:2506.12928.
4. EpiCaR (2025). Knowing What You Don't Know Matters for Better Reasoning. arXiv:2601.06786.
