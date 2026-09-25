# Agent Memory Intelligence

## Overview

ggcode implements a comprehensive memory intelligence system inspired by
MemGPT (hierarchical memory), Mem0 (memory management), and Anthropic's
Memory Best Practices. All intelligence features are zero-LLM-cost
deterministic implementations using pattern matching.

## Memory Lifecycle

```
Create → Classify → Inject → Curate → Staleness Check → GC
  │         │          │          │           │            │
  │    transient/  auto-inject  expire/    broken-path   disk
  │    evolving/   persistent  dedup/     oversized     cleanup
  │    persistent   + index    cap        ancient
```

## Features

### Memory Classification (`curation.go`)
- **Transient**: one-time task records (impl-task-*, *-fix, *-bug) - expire after 30 days
- **Evolving**: research/analysis superseded by newer versions (competitor-*, research-*) - same-prefix dedup keeps only latest
- **Persistent**: architecture decisions, design docs (*-impl, *-design) - never expire
- **Default**: general memories - capped at 60 total active entries

### Memory Staleness Detection (`staleness.go`)
Detects three types of staleness in memory entries:
1. **Broken file paths**: References to files/dirs that no longer exist in the project
2. **Oversized entries**: Content exceeding the inline budget (1200 bytes)
3. **Ancient persistent**: Entries older than 180 days that may be outdated

```go
report := am.ScanStaleness(workingDir)
// report.BrokenPaths, report.Oversized, report.Ancient
```

### Duplicate Detection (`duplicate_check.go`)
Prevents redundant memory creation by checking similarity before save:
- Uses Jaccard token-set similarity on memory keys
- Threshold: 0.6 similarity triggers a warning
- Integrated into `save_memory` tool — warns agent when creating near-duplicates

```go
dc := am.CheckDuplicate(key, content)
if dc.IsDuplicate() {
    // dc.SimilarTo, dc.Similarity, dc.ExistingContent
}
```

### Memory Health Report (`health_report.go`)
Diagnostic dashboard showing:
- Total/active/expired/deduped/capped counts
- Category distribution (persistent/evolving/transient/default)
- Context budget usage (inline bytes vs limit)
- Age range (oldest-newest entry in days)
- Staleness signals (broken paths, oversized, ancient)
- Potential duplicate groups

```go
report := am.HealthReport(workingDir)
fmt.Println(report.FormatHealthReport())
```

### Memory Curation (`curation.go`, `gc.go`)
- **Expiry**: transient entries removed after 30 days
- **Dedup**: evolving entries with same prefix keep only newest
- **Cap**: max 60 active entries (oldest defaults evicted first)
- **GC**: physical disk cleanup of expired/deduped files

### Auto-Injection (`auto.go`)
- Persistent entries (small enough) inlined directly into system prompt
- All other entries listed as title-only index for on-demand read_file
- Budget: 6000 bytes total inline (~1500 tokens)

### Ratchet Rule Staleness Sweep (`ratchet.go`)
The learned-rules store (`.ggcode/agent-rules.json`) previously declared a
staleness policy (`staleRuleThreshold` = 30 days, `staleRuleMinHits` = 3) as
constants but never executed it: stale low-value rules survived forever and
competed for the prompt injection slots. A consolidation sweep now runs at
the end of every run (inside `runRatchet`, before the error-matching early
return, so quiet runs also sweep):
- `CleanStale()` removes rules not seen in 30 days unless they have >= 3
  hits (proven lessons are preserved indefinitely)
- `DeduplicateRules()` merges near-duplicate rules that accumulated since
  the last sweep

This implements the "write-manage-read" memory loop (arXiv:2603.07670):
stored lessons need periodic management sweeps, not just LRU eviction when
the 60-slot cap overflows.

### Task-Selective Rule Injection (`ratchet.go`, `agent_prompt_inject.go`)
`TopRulesForPrompt(N)` injected the global top-N rules regardless of what
the user asked for. Injection is now task-aware (`TopRulesForTask`):
1. The latest user message is classified with the playbook's
   `classifyTaskType` (plus a CJK keyword supplement, since the Latin
   word-boundary check cannot anchor CJK keywords against adjacent ASCII).
2. Task type maps to relevant rule categories: bugfix/build/test ->
   `build`+`test`, refactor/feature -> `convention`+`build`, review ->
   `convention`+`security`, git prompts (CJK) -> `git`.
3. A global floor of the top-2 recency-weighted rules is always kept so
   critical high-hit lessons survive even when out of scope; remaining
   slots go to task-relevant rules. No backfill: unfilled slots are the
   token saving.

Rationale: token-efficient retrieval (Mem0 "State of AI Agent Memory"
2026; arXiv:2603.07670 read-path optimization) - a small task-relevant
lesson block beats a global top-N dump, and the rules live in the
non-cacheable dynamic prompt layer, so every injected irrelevant rule is
re-paid on every run. Prompts that classify as "other" (or are empty, e.g.
before the first turn) fall back to the unfiltered global top-N.

## Competitor Comparison

| Feature              | ggcode | Claude Code | Cursor | Devin |
|---------------------|--------|-------------|--------|-------|
| Persistent memory    | Yes    | CLAUDE.md   | .cursorrules | Yes  |
| Auto-classification  | Yes    | No          | No     | No    |
| Expiry/TTL           | Yes    | No          | No     | No    |
| Dedup                | Yes    | No          | No     | No    |
| Staleness detection  | Yes    | No          | No     | No    |
| Duplicate detection  | Yes    | No          | No     | No    |
| Health dashboard     | Yes    | No          | No     | No    |
| Budget management    | Yes    | No          | No     | No    |
