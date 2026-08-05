# Quality Regression Detection (Eval-Driven Development)

## Background

Eval-Driven Development is the 2025-2026 core methodology for systematically
measuring AI agent quality and driving continuous improvement. Representative
platforms: LangSmith Evals (trajectory evaluation), Braintrust (experiment
tracking + regression alerts), OpenAI Evals Framework, DeepEval, Phoenix/Arize.

A central capability of every eval platform is **regression detection**:
flagging when a run's quality has dropped significantly below the historical
baseline, so teams catch quality degradation early rather than discovering it
weeks later.

## Gap in ggcode

`ResponseQualityScorer` (`response_quality.go`) recorded per-run quality
metrics and compared providers A/B, but it **never compared the latest run
against its own rolling historical baseline**. So a slow quality erosion (from
a config change, model swap, or accumulating context bloat) went completely
silent. This detector fills that gap.

## Implementation

`quality_regression.go` adds `DetectRegression()` to `ResponseQualityScorer`:

1. After each scored run, builds a rolling baseline (mean over the previous N
   runs for the same provider/model pair, up to 10 runs)
2. Classifies the current run's deviation across three dimensions:
   - **Overall score regression** (current << baseline mean)
   - **Iteration inflation** (current iteration ratio >> baseline) -- the most
     actionable leading indicator of degrading agent efficiency
   - **Error-rate regression** (current error rate >> baseline)
3. Assigns a severity tier: `none`, `minor`, `moderate`, `severe`

Wired into `reflection.go` `maybeReflect()` via `maybeDetectRegression()` --
fires after each `ScoreRun` call, emits a `debug.Log` when regression is
detected, and stores the latest report on the scorer.

## Design Decisions

- **Per-provider/model isolation**: baselines are scoped to the same
  provider/model pair, so switching models doesn't cross-contaminate
- **Minimum history threshold** (3 runs): below this, variance noise dominates
- **Rolling window** (10 runs): keeps the baseline responsive to recent trends
- **Advisory only**: never blocks the run -- follows existing gate/check pattern
- **Zero LLM cost**: all signals are deterministic heuristics derived from
  RunStats, consistent with the existing scorer design
- **Iteration ratio as leading indicator**: iteration count inflation is the
  earliest detectable signal of degrading efficiency (fires before score
  drops enough to trigger the score-based check)

## Competitor Analysis

| Platform       | Regression Detection | Trajectory Eval | Iteration Trend |
|----------------|---------------------|-----------------|-----------------|
| LangSmith      | Yes (threshold)     | Yes             | Partial         |
| Braintrust     | Yes (alerts)        | Yes             | Partial         |
| Claude Code    | No                  | No              | No              |
| Cursor         | No                  | No              | No              |
| OpenHands      | Partial             | Yes             | No              |
| **ggcode**     | **Yes (this)**      | Partial (HTC)   | **Yes**         |

## Files

- `internal/agent/quality_regression.go` -- detector implementation
- `internal/agent/quality_regression_test.go` -- 11 tests
- `internal/agent/response_quality.go` -- added `latestRegression` field
- `internal/agent/reflection.go` -- wired `maybeDetectRegression()` call
