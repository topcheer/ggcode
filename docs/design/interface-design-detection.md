# Go Interface Design & Abstraction Intelligence

## Overview

Deterministic, AST-based detection of Go interface design anti-patterns at write time. This check catches design smells that harm maintainability and type safety but that the Go compiler and most linters only detect on-demand (not at edit time).

## Motivation

Go's implicit interface implementation is powerful but error-prone. The compiler cannot detect:
- Interfaces bloated with too many methods (ISP violation)
- Overly generic method names that reduce clarity
- Type-unsafe `any`/`interface{}` at abstraction boundaries
- Exported interfaces with unexported methods (broken encapsulation)
- Unnecessary abstractions (interfaces with only one implementation)

These issues compound over time, making code harder to test, mock, and maintain.

## Competitor Mapping

| Tool | Interface Design Analysis | When |
|------|--------------------------|------|
| golangci-lint (`interfacebloat`, `iface`) | Fat interface, naming | On-demand (CI/lint) |
| Claude Code / Cursor | None (relies on LSP/build) | Post-build |
| GitHub Copilot | None | N/A |
| OpenHands / Devin | None | N/A |
| **ggcode** | **6 checks, delta-aware, real-time** | **At write time** |

## Checks Implemented

### 1. Fat Interface (ISP Violation)
- **Rule**: Interfaces with >7 methods violate the Interface Segregation Principle.
- **Threshold**: `idMaxInterfaceMethods = 7` (configurable constant).
- **Action**: Advise splitting into smaller, focused interfaces.

### 2. Non-Idiomatic Single-Method Interface Naming
- **Rule**: Single-method interfaces should use method names that derive from the interface name (Reader -> Read, Closer -> Close).
- **Detection**: Generic method names (`Do`, `Run`, `Execute`, `Process`, `Handle`, `Perform`, `Call`, `Apply`, `Work`, `Action`, `Operate`, `Invoke`) that don't prefix the interface name.
- **Idiomatic check**: `strings.HasPrefix(lowerIface, lowerMethod)` passes (e.g., "reader" starts with "read").

### 3. Returning `any` / `interface{}`
- **Rule**: Exported functions returning `any` or `interface{}` erase type safety at every call site.
- **Scope**: Only exported functions (capitalized names) are flagged.

### 4. Interface Method Using `any` / `interface{}`
- **Rule**: Interface methods with `any` params or returns weaken the type contract at the abstraction boundary.

### 5. Exported Interface with Unexported Method
- **Rule**: An exported interface containing unexported methods restricts implementations to the current package, defeating the purpose of exporting.
- **Action**: Make the method exported, or unexport the interface.

### 6. Single Implementation (Unnecessary Abstraction)
- **Rule**: A newly-introduced interface with >=2 methods that has only one implementer in the package may be over-abstraction.
- **Threshold**: `idMinMethodsForSingleImpl = 2` (single-method interfaces are commonly used for mocking/testing and are excluded).
- **Scope**: Only newly-introduced interfaces (delta-aware) to avoid re-reporting existing design decisions.

## Design Properties

### Delta-Aware
Each check compares the new content against old content and only reports issues that are **newly introduced** by the edit. Pre-existing problems are not re-flagged, keeping the signal-to-noise ratio high.

### Bounded Output
At most `idMaxWarnings = 5` warnings per file, preventing context overflow.

### Language Filtered
Only runs on `.go` files (`LangGo`). Test files (`_test.go`) are skipped.

### Zero LLM Cost
All checks are pure AST pattern matching -- no LLM calls, no external tools, no network.

### Non-Blocking
Warnings are advisory -- they are injected into the tool result but the edit is never reverted.

## Architecture

```
checkInterfaceDesign(ctx CheckContext) []string
  |-- idCheckFatInterfaces       (ISP violation)
  |-- idCheckNaming              (non-idiomatic method names)
  |-- idCheckInterfaceAny        (any in interface methods)
  |-- idCheckUnexportedMethod    (exported interface + unexported method)
  |-- idCheckReturningAny        (returning any/interface{})
  |-- idCheckSingleImpl          (single implementation)
```

Each check function takes the pre-parsed `*ast.File` (shared from `CheckContext.GoAST`) and the old-content metadata maps. All are independent and can run in parallel within the check registry framework.

## File Organization

| File | Purpose |
|------|---------|
| `internal/agent/interface_design_check.go` | Implementation (~520 lines) |
| `internal/agent/interface_design_check_test.go` | Tests (27 tests) |
| `internal/agent/write_integrity.go` | Registration (1 entry in `allChecks`) |

## Cyclomatic Complexity

All functions are kept under 15 cyclomatic complexity. The largest functions are split into helper functions:
- `idCheckSingleImpl` delegates to `idCollectPackageTypes` and `idCountImplementers`
- `idCheckNaming` delegates to `idNamingIssue`
- `idCheckInterfaceAny` delegates to `idInterfaceUsesAny` -> `idFuncTypeUsesAny`

## Testing

27 tests covering:
- Each check's positive case (issue detected)
- Each check's negative case (no false positives)
- Delta-awareness (pre-existing issues not re-flagged)
- Max warnings cap
- Language filtering (non-Go files skipped)
- Test file skipping
- Utility functions (direct unit tests)
