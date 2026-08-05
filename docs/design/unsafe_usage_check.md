# Design: Unsafe Package Usage Detection (sa-61)

## Trend: Memory Safety in AI-Generated Go Code

The `unsafe` package is Go's #1 source of memory safety violations. As AI
coding agents increasingly suggest "optimized" code (zero-copy conversions,
manual pointer arithmetic), they introduce subtle bugs that are:

- **Not caught by `go vet`**: `go vet -unsafeptr` only detects one narrow
  pattern (uintptr stored in a variable and converted back to Pointer)
- **Non-deterministic**: bugs manifest only when the GC moves objects
- **Silent**: code compiles, tests may pass, production crashes

## Competitor Analysis

| Product | Write-time unsafe detection | Coverage |
|---------|---------------------------|----------|
| Claude Code | None | - |
| Cursor | Relies on go vet -unsafeptr | 1 pattern only |
| Cline/OpenHands | None | - |
| Aider | None | - |
| Devin | None | - |
| **ggcode** | **AST-based, 3 patterns** | **This check** |

## Three Patterns Detected

### 1. Pointer Arithmetic via uintptr (GC hazard)
```go
// DANGEROUS: GC may move object between conversions
return unsafe.Pointer(uintptr(p) + offset)

// SAFE: use unsafe.Add (Go 1.17+)
return unsafe.Add(p, offset)
```

### 2. Deprecated reflect.SliceHeader / StringHeader
```go
// DEPRECATED since Go 1.20: undefined behavior
hdr := (*reflect.SliceHeader)(&slice)

// SAFE: use unsafe.Slice / unsafe.String
s := unsafe.Slice(ptr, n)
```

### 3. Stored uintptr from unsafe.Pointer
```go
// DANGEROUS: uintptr is not GC-tracked
offset := uintptr(unsafe.Pointer(p))

// SAFE: keep as unsafe.Pointer
offset := unsafe.Pointer(p)
```

## Design Decisions

1. **AST-based**: Pure AST walk, no LLM cost, sub-millisecond execution
2. **Delta-aware**: Only flags patterns newly introduced by the current edit
3. **Test files skipped**: `_test.go` files often legitimately use unsafe
4. **Max 3 warnings**: Capped to avoid flooding the agent's context window
5. **Category-prefixed warnings**: `[Unsafe]` prefix for clear filtering
6. **Line numbers**: Includes line number for precise navigation
7. **No new dependencies**: Uses only Go stdlib (go/ast, go/parser, go/token)

## Complexity

All functions maintain cyclomatic complexity below 15. The AST traversal
delegates to small helper functions (`checkUnsafePointerArith`,
`checkReflectHeader`, `isUintptrOfUnsafePointer`) to stay under the limit.

## Files

- `unsafe_usage_check.go` (240 lines) - check implementation
- `unsafe_usage_check_test.go` (200 lines) - 13 tests, all passing
- `write_integrity.go` - 1 line registration

## Registration

```go
{Name: "unsafe-usage", Langs: []Language{LangGo}, Run: sliceCheck(checkUnsafeUsage)},
```
