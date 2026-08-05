# Copylock Detection (Check #54)

## Problem

AI coding agents frequently produce Go code that passes `sync` types by value
instead of by pointer. Go's sync primitives (`sync.Mutex`, `sync.RWMutex`,
`sync.WaitGroup`, `sync.Cond`, `sync.Once`, `sync.Map`, `sync.Pool`) contain
internal state that **must not be copied**. Copying silently breaks locking
semantics:

- Two copies of a `Mutex` protect different state -- no mutual exclusion.
- Copying a `WaitGroup` loses the counter, causing `Add/Done/Wait` mismatches.
- Copying a `sync.Once` can cause initialization to run twice.

`go vet -copylocks` catches this post-build, but no AI coding agent detects it
at **write time**.

## Competitor Analysis

| Product | Write-time detection | Notes |
|---|---|---|
| Claude Code | No | Relies on go vet post-save |
| Cursor | No | go vet may catch on save, inconsistent |
| Cline/OpenHands | No | Reactive only |
| Aider | No | |
| Windsurf | No | |
| GitHub Copilot | No | |
| **ggcode** | **Yes** | Zero-dependency AST analysis at write time |

## Approach

AST-based analysis using Go's standard `go/parser` and `go/ast` packages. For
each function declaration, the check inspects:

1. **Value parameters**: `func f(m sync.Mutex)` -- the mutex is copied.
2. **Value return types**: `func g() sync.Mutex` -- the caller gets a copy.
3. **Value receivers**: `func (s Server) Lock()` -- if `Server` embeds
   `sync.Mutex`, a value receiver copies it.

Additionally, a first pass collects named struct types that contain sync fields
by value (e.g., `type Server struct { mu sync.Mutex }`). Functions that receive,
return, or are defined on these structs by value are also flagged.

### Detection Details

- **sync types detected**: `sync.Mutex`, `sync.RWMutex`, `sync.WaitGroup`,
  `sync.Cond`, `sync.Once`, `sync.Map`, `sync.Pool`
- **Pointer types are NOT flagged**: `*sync.Mutex` is correct usage.
- **Struct embedding detection**: structs with sync fields by value are
  collected and tracked; passing them by value triggers the warning.
- **Delta-aware**: only flags NEW violations introduced by the edit.
- **Max warnings**: 4 per write + truncation notice.

## Files

- `internal/agent/copylock_check.go` -- implementation (230 lines)
- `internal/agent/copylock_check_test.go` -- 13 tests
- `internal/agent/write_integrity.go` -- registration (1 line)

## Relationship to Existing Checks

- `lock_without_unlock_check.go`: detects missing `Unlock()` after `Lock()`.
  Its header explicitly notes it does NOT cover copylock. This check fills
  that gap.
- `goroutine_leak_check.go`: detects unmanaged goroutines. Complementary --
  goroutine lifecycle is separate from sync value copying.
- `waitgroup_misuse_check.go`: detects WaitGroup API misuse (Add after Wait,
  etc.). This check catches the structural issue of passing WaitGroup by value.
