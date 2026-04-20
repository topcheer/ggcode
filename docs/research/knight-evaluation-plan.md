# Knight evaluation plan

This plan answers two questions:

1. **Can Knight run real background work safely on a real provider?**
2. **Does Knight measurably improve ggcode instead of just looking busy?**

Use this plan when delegating validation to other agents. The goal is to make every run use the same phases, inputs, and scoring sheet.

## 1. Preconditions

1. A real provider credential must be configured for the main agent.
2. If Knight should use a different provider, configure any subset of:
   - `knight.vendor`
   - `knight.endpoint`
   - `knight.model`
3. Any missing Knight field automatically falls back to the main agent selection.
4. Start with `knight.trust_level: staged` for all real-provider runs.

Example:

```yaml
vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo

knight:
  enabled: true
  trust_level: staged
  model: glm-5-air
  idle_delay_sec: 60
  capabilities:
    - skill_creation
    - skill_validation
    - regression_testing
    - doc_sync
```

## 2. Phase 1: regression gate

Run this first on every candidate branch:

```bash
make knight-eval
```

This creates a bundle under `.tmp/knight-eval-*` and runs:

1. `./scripts/dev/verify-ci.sh`
2. `go test ./internal/knight ./internal/tui ./cmd/ggcode`

Do not proceed to real-provider testing if this phase fails.

## 3. Phase 2: real-provider smoke test

Goal: prove Knight can run against a real provider without runaway behavior.

Use one real workspace and one temporary test workspace.

### Steps

1. Start ggcode with Knight enabled and `trust_level: staged`.
2. Drive **3 short sessions** in the same workspace that intentionally repeat one workflow. Recommended patterns:
   - build -> test -> inspect failure -> fix -> retest
   - docs read -> edit -> verify command references
   - lint -> fix -> rerun
3. Leave the session idle long enough for Knight analysis to trigger.
4. Record:
   - whether a staged skill appears
   - whether the staged skill is relevant
   - whether Knight reports are understandable and non-spammy
   - whether any unexpected background work occurs

### Smoke pass criteria

1. No crashes, stuck loops, or budget runaway
2. At most one staged skill per repeated pattern family unless clearly justified
3. No unsafe file/path behavior
4. Reports are actionable, not chatty

## 4. Phase 3: quantitative A/B benchmark

Goal: measure if Knight-created skills improve ggcode on repeated tasks.

### Test design

Use a fixed task set of **10 tasks** from the same repo:

1. 4 code-edit tasks
2. 3 test/debug tasks
3. 3 docs/process tasks

Run each task twice:

1. **Baseline**: Knight disabled
2. **Knight-assisted**: Knight enabled, staged skills approved if clearly relevant

### Record for every task

- success / failure
- number of user turns
- number of tool calls
- wall-clock seconds
- input/output tokens
- number of retries / rework loops
- whether a Knight-generated skill was used

Write every row into the `metrics.csv` created by `make knight-eval`.

### Recommended metrics

For each mode, calculate:

1. **Task success rate** = successful tasks / total tasks
2. **Median elapsed seconds**
3. **Median tool calls per task**
4. **Median turns per task**
5. **Rework rate** = tasks requiring repeated correction / total tasks
6. **Useful skill rate** = tasks where a Knight-generated skill clearly reduced work / tasks where Knight skill was used

### A/B acceptance thresholds

Treat Knight as promising only if all are true:

1. Success rate is **not lower** than baseline
2. Median elapsed time is **>= 10% better** or unchanged with better consistency
3. Rework rate is **not higher** than baseline
4. Useful skill rate is **>= 60%**

## 5. Phase 4: background soak run

Goal: validate that Knight behaves well over time, not just in one short demo.

### Setup

1. Use one real repo for **1-3 days**
2. Keep `trust_level: staged`
3. Enable the full capabilities set intended for real usage
4. Do normal development work; do not artificially manufacture every prompt

### Record daily

- staged skill count
- approved skill count
- rejected skill count
- patched skill count
- rollback count
- nightly audit usefulness (0-2)
- spam incidents
- obviously wrong candidate count

### Soak pass criteria

1. No duplicate-skill storms
2. No repeated low-value/nightly spam
3. Rejected skills stay a minority of staged skills
4. Rollbacks are rare
5. Operators still trust the staged queue after the run

## 6. How to judge “Knight makes ggcode smarter”

Do **not** use only internal activity measures like “number of skills generated”.

Use a weighted score focused on task outcomes:

```text
Knight Score
= 35% task success rate delta
+ 20% elapsed time improvement
+ 15% tool call reduction
+ 10% turn reduction
+ 10% useful skill rate
+ 10% operator trust score
```

Where:

- **task success rate delta** = Knight mode success rate - baseline success rate
- **elapsed/tool/turn improvements** are normalized against baseline
- **operator trust score** is a 1-5 daily human rating converted to 0-100

### Suggested interpretation

- **>= 75**: Knight is materially helping
- **60-74**: promising, but not proven
- **40-59**: mixed / unstable
- **< 40**: not ready for background autonomy claims

## 7. Mandatory artifacts from every evaluation run

Every agent running this plan should produce:

1. `.tmp/knight-eval-*/baseline.log`
2. `.tmp/knight-eval-*/metrics.csv`
3. `.tmp/knight-eval-*/scorecard.md`
4. a short narrative summary of:
   - best win
   - worst failure
   - whether Knight improved real tasks or just generated activity

## 8. Recommended next improvements if evaluation fails

1. **Too much noise** -> tighten candidate prioritization and staging thresholds
2. **Wrong skills** -> improve session analyzer ranking and usefulness signals
3. **No measurable benefit** -> add a task-outcome evaluator instead of relying mainly on success/failure heuristics
4. **Good short demos, bad soak** -> add queue aging, stronger anti-repeat suppression, and richer nightly audit scoring
