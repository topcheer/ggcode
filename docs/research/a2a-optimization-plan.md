# A2A Optimization Plan

## Phase 1: Correctness (data races + deadlocks)
- [P1-1] H5: GetTask returns pointer → deep copy
- [P1-2] H1/H2: Polling → per-task done channel + ctx propagation
- [P1-3] H3: handleTaskCancel returns stale snapshot
- [P1-4] H4: generateID collision-safe
- [P1-5] M5: Unbounded task map → cleanup expired tasks
- [P1-6] M4: continueTask TOCTOU → set working state in locked section

## Phase 2: Performance (file contention)
- [P2-1] M2: Registry → per-PID files

## Phase 3: Robustness
- [P3-1] M3: RemoteTool ambiguous match → error with candidates
- [P3-2] M6: SSE multi-line data parsing
