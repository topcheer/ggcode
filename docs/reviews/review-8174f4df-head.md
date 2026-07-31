# Code Review: 8174f4df..HEAD (last 2 commits)

**Reviewer**: subagent (deepseek-v4-flash, same model as main agent)
**Commits**:
- 80257c8f perf: background section collector for zero-IO system prompt rebuild
- a781e1c8 perf: desktop also uses background section collector
**Date**: 2026-08-01

## Verdict: SHIP WITH FIXES

Design verified sound: RWMutex-protected snapshot with full-string replacement (no torn reads), synchronous first refresh (initial prompt has data), per-section timeouts, bounded synchronous Stop. `workingDir` is immutable today so wrong-dir reads don't trigger in production.

---

## MEDIUM

### M1. Data race on package-global `globalSectionCollector` pointer
- **Evidence**: `internal/agentruntime/section_collector.go:65,70,80,86-87,164-168`
- **Commit**: 80257c8f (worsened by a781e1c8 for desktop)
- **Detail**: `InitGlobalSectionCollector`/`StopGlobalSectionCollector`/`GlobalSectionSnapshot` read/write the package var with no lock or atomic. Desktop `ChatBridge.Close()` (chat.go:2985) calls `StopGlobalSectionCollector()` before cancelling in-flight sub-agents — a concurrent prompt build can read the pointer while Close writes nil. Second hazard: two goroutines calling `InitGlobalSectionCollector` with different workingDir both pass the `!= nil` guard, both call `Stop()` on the old collector, second `close(sc.stop)` panics "close of closed channel".
- **Fix**: guard with `sync.RWMutex` or `atomic.Pointer[SectionCollector]`; make `Stop` idempotent via `sync.Once` around `close(sc.stop)`.

### M2. Unconditional 10-second background I/O even when idle
- **Evidence**: `internal/agentruntime/section_collector.go:106,111-114` + `refresh()` at 137-151
- **Commit**: 80257c8f
- **Detail**: `loop` fires `refresh()` every 10s forever regardless of activity. Each tick spawns `git status` twice (up to ~4s) + Go AST parsing (200ms). Previously these ran only at prompt-build time. A session left open overnight keeps spawning git subprocesses + parsing every 10s indefinitely.
- **Fix**: lazy invalidation — refresh on-demand before a build when cache is stale; or gate expensive work behind an "agent active" flag.

### M3. New concurrency primitive ships with zero tests
- **Evidence**: `internal/agentruntime/section_collector.go` (whole file, 169 lines)
- **Commit**: 80257c8f
- **Detail**: No test coverage of goroutine start/stop, RefreshNow, Snapshot under -race, or workingDir-mismatch guard. Risk profile (leaks, races, double-collection) is exactly what a `go test -race` lifecycle test catches.
- **Fix**: add `section_collector_test.go` — init, assert snapshot populated, Stop and assert loop exits, run under -race with concurrent Snapshot/RefreshNow.

---

## LOW

- **L1**: `RefreshNow` is dead code (no callers) and spawns untracked goroutines — remove or add coalescing + lifecycle tracking. `section_collector.go:124-126`
- **L2**: "zero-IO" claim overstated — prompt build still reads named-agent templates, skills, memory index from disk on every submit (`prompt.go:117`, `BuildSkillsSystemPromptWithPromptRefs`, `appendAutoMemory`→`LoadIndex`). Only the 5 sections moved to collector.
- **L3**: Wrong-directory snapshot is latent if workingDir ever becomes mutable — collector keys on stored dir while builders use a param (`section_collector.go:65` vs `prompt.go:67-72,251-256`). Consider asserting `sc.working == requestedDir`.
- **L4**: `Stop()` can block shutdown up to ~4s waiting for mid-refresh git commands (`section_collector.go:97-101`) — bounded, not a deadlock; consider timeout on `<-sc.done`.

---

## Verification performed
- Goroutine lifecycle traced (sync-refresh-before-Start, Stop closes stop + blocks on done, loop defers close(done))
- All section functions independently bounded
- All call sites (root.go, daemon.go, desktop wailskit) use same fixed workingDir, all Stop via defer/Close
- No double-init in a single process (daemon --background forks before runDaemon)
- workingDir immutable per session (set once from os.Getwd(), no runtime cd mutation found)
- `go vet` and `go build -tags goolm` pass
