# Playbook Hint Task-Relevance Filtering (Progressive Disclosure)

**Rotation**: r81 frontier research round
**Status**: Implemented

## Problem

The Strategy Playbook records successful run patterns keyed by task type
(`bugfix`, `feature`, `refactor`, `review`, `test`, `build`, `other`), but
`HintsForPrompt` selected the top-3 entries purely by a global
frequency × efficiency score. The injected hints ignored which task the
current run is actually performing:

- A bugfix session could be shown feature-building tool sequences ahead of
  proven bugfix sequences.
- A heavily-used but inefficient pattern could crowd out the single most
  relevant strategy for the task at hand.
- The `TaskType` field was written at record time but never used on the read
  path.

Users perceive this as generic, sometimes misleading "learned strategy"
hints that do not match the job being done.

## Frontier basis

- **Progressive disclosure / selective loading** — Anthropic Agent Skills
  (Oct 2025) and "Is Progressive Disclosure All You Need for Long-Context
  Agents?" (arXiv:2607.17598): keep only short, task-relevant summaries in
  context and spend the context budget on what matches the current request.
- **Self-evolving experience retrieval** — MUSE-Autoskill (arXiv:2605.27366)
  and EvolveR (openreview so9LoD9VSf): distilled experience (playbooks /
  skills) is retrieved by task relevance, not global popularity, as part of
  an explicit lifecycle (creation → memory → management → evaluation).

## Design

`Playbook.HintsForTask(maxHints int, taskType string)` replaces the ranking
logic:

1. Entries whose `TaskType == taskType` rank first; within each group
   (matching / non-matching) entries are ordered by the existing composite
   score `min(uses, 10) × (10 / avgIter)` (SICA-inspired, arXiv:2504.15228).
2. Remaining slots are backfilled with the best-scoring entries of other
   task types, so the hint budget is never wasted.
3. `taskType == ""` preserves the legacy pure-score ordering
   (`HintsForPrompt` remains as a thin wrapper for compatibility).
4. When at least one leading entry matches, the header notes the relevance
   ("leading entries match the current bugfix task") so the model knows why
   those sequences are surfaced.

The injection point (`maybeInjectDynamicSystemPrompt`, layer 4) now receives
the current run's user prompt and derives the task type with the existing
read-only `classifyTaskType` (keyword set unchanged, per #2745). The system
prompt is rebuilt per run, so the selected hints track the current task
without any persistent state changes. The playbook file format and record
path are untouched.

## Files

- `internal/agent/playbook.go` — `HintsForTask`, `HintsForPrompt` wrapper
- `internal/agent/agent_prompt_inject.go` — layer 4 task-type-aware injection
- `internal/agent/agent.go` — pass `userPromptForStats` to the injector
- `internal/agent/playbook_test.go` — `TestPlaybookHintsForTaskRelevance`

## Tests

`go test -tags goolm -run 'TestPlaybook|TestClassifyTaskType|TestMaybeInjectDynamicSystemPrompt' ./internal/agent`

The new test verifies that a low-score bugfix entry outranks a high-score
feature entry for a bugfix prompt, and that unrelated task types keep the
legacy score ordering and legacy header.
