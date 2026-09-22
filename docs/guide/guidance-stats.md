# Guidance Stats (Detector Guidance Effectiveness Telemetry)

ggcode's detectors emit tagged guidance hints (e.g. `[STRATEGY-STAGNATION] ...`)
that are injected into the conversation when they fire. Until now there was no
longitudinal record of *which* hints actually fire, how often the same hint has
to fire repeatedly (guidance that does not stick), or how often users push back
right after a hint fires. `guidance-stats` closes that gap with a deterministic,
zero-LLM-cost feedback loop:

```
capture (delivered hints, per tag) -> join (user negative signals) -> report (Wilson 95% intervals)
```

## Methodology

The implementation follows current statistical-rigor guidance for AI agent
evaluation:

- **Report counts with intervals, not point estimates.** Every rate is shown as
  `rate[lo,hi]` using the Wilson 95% score interval (preferred over the Wald
  interval near 0/1 and at modest sample sizes).
- **Zero observed failures is not zero risk.** A tag with `0` negative hits and
  `n` fires gets an explicit upper confidence bound instead of a bare "clean".
- **Small samples are not judged.** Tags below 5 fires are labeled
  `insufficient evidence` rather than ranked.
- **Independence is not assumed.** Events inside one session are clustered;
  the report states that the raw count is an upper bound on effective sample
  size.

Research sources:

- ICLR Blogposts 2026, *Why AI Evaluations Need Statistical Rigor* —
  https://iclr-blogposts.github.io/2026/blog/2026/why-ai-evaluations-need-error-bars/
- S. Yang, *Statistical Confidence for AI Agent Evaluations* (2026, incl. NIST
  AI 800-3 binomial-interval guidance) —
  https://stanleycyang.com/writing/statistical-confidence-agent-evals
- FutureAGI, *LLM Eval Feedback Loop Design 2026* (capture -> join -> calibrate
  -> gate; the join step most teams skip) —
  https://futureagi.com/blog/llm-eval-feedback-loop-design-2026/

## What is measured

| Metric | Definition | Interpretation |
|--------|-----------|----------------|
| Fires | Delivered tagged hints per detector tag (after budget/dedup gating — what the model actually saw) | exposure |
| Repeats | Fires of the same tag within 10 min of its previous fire | guidance did not stick (ineffectiveness proxy) |
| Negative hits | Fires within 6 min before a user negative signal (textual negative feedback or user file revert); each fire attributed at most once | annoyance / false-positive proxy |

Both delivery paths are instrumented: iteration-level `injectGuidance` and the
tool-result hint path (`appendGuidance` / `applyToolResultGuidance`). Untagged
loop-recovery nudges are excluded. Data is purely observational — nothing is
auto-suppressed based on these stats; consolidation decisions stay
human-reviewed.

## Usage

Per-run aggregates are appended (best-effort) to `<project>/.ggcode/guidance-stats.jsonl`
at the end of each agent run. Inspect the offline aggregate with:

```bash
ggcode guidance-stats
```

Example output:

```
TAG                             FIRES  REPEATS    NEG  REPEAT 95%        NEGATIVE 95%      NOTES
STRATEGY-STAGNATION                12        5      2  0.42[0.19,0.68]   0.17[0.05,0.41]   inconclusive
hardcoded-secret                    8        0      0  0.00[0.00,0.32]   0.00[0.00,0.32]   clean (0 neg hits, 95% upper 0.32)
MY-NEW-DETECTOR                     3        1      0  -                 -                 insufficient evidence (n=3)
```

## Limitations

- Temporal attribution is heuristic: a negative user message inside the
  attribution window is a proxy, not a proven causal link to a specific hint.
- Per-session clustering means wide intervals are expected early; collect more
  runs before acting on a tag.
- Report-only by design; the JSONL is local to the project and is not uploaded.
