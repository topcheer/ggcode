# Nil Dereference After Error Detection

## Overview

Go's most common runtime panic is the nil pointer dereference. The canonical
pattern that causes it:

```go
result, err := someFunc()
fmt.Println(result.Field) // PANIC if err != nil and result is nil
```

When `err != nil`, many Go functions return `nil` for their primary return
value. Dereferencing that nil pointer crashes the program with:
`panic: runtime error: invalid memory address or nil pointer dereference`.

## What This Check Detects

The `nil-deref-after-error` check (registered in `write_integrity.go`) scans Go
code at write time for the following pattern within each function body:

1. A multi-return assignment where the last value is an error:
   `v, err := someFunc()`
2. The variable `v` is dereferenced (via `.Field`, `.Method()`, `[idx]`, or `*v`)
   **before** an `if err != nil` guard appears in source order.

### Supported dereference forms

| Pattern | Example | Risk |
|---------|---------|------|
| Selector | `v.Field` | nil struct dereference |
| Method call | `v.Method()` | nil receiver call |
| Index | `v[0]` | nil slice/map/pointer-to-array |
| Star | `*v` | nil pointer dereference |

## What It Does NOT Flag

- Variables from single-return assignments (no error involved)
- Variables that are simply read, not dereferenced
- Dereferences that appear **after** an `if err != nil` guard
- Test files (lower risk, test failures already stop execution)

## How It Works

The check uses Go's `go/ast` package to parse the file and walk each function
body in source order. It maintains a `nilRisk` set of variable names from
multi-return assignments where the last value is an error. When an `if err != nil`
block is encountered, the set is cleared (the error has been handled). Any
dereference of a variable still in the nil-risk set triggers a warning.

The check is **delta-aware**: it compares instance counts between old and new
content and only flags patterns newly introduced by the current edit.

## Competitor Analysis

| Tool | Detection | Write-Time | Cost |
|------|-----------|------------|------|
| **ggcode (this check)** | AST pattern matching | Yes | Zero (deterministic) |
| staticcheck SA5011 | SSA analysis | No (lint cycle) | Full type info required |
| gosec G601 | Range loop aliasing | No | Separate tool |
| Uber nilaway | Dataflow analysis | No | Separate tool |
| go vet | Does NOT detect | N/A | N/A |

## Example Warning

```
[Nil dereference after error] The following pointer(s) are dereferenced before
checking the error return, which can cause a nil pointer panic:
  - main.go:15: 'r' is dereferenced before an 'if err != nil' check.
    When the error is non-nil, functions often return nil for the primary value.
    Add `if err != nil { return err }` before using 'r'.
```

## Related Checks

- **nil-map-write**: Detects writes to uninitialized maps
- **unchecked-type-assert**: Detects `x.(T)` without comma-ok guard
- **panic-safety**: Detects bare `panic()` in library code
- **ignored-error-return**: Detects completely discarded error returns
- **range-copy-mod**: Detects modification of range loop copy variables
