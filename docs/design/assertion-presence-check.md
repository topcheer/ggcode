# Hollow Test Detection (Assertion Presence Verification)

## Trend

AI agents frequently generate test functions that look plausible but contain zero assertions -- they call the code under test but never verify the result. These "hollow tests" pass trivially, give false confidence, and mask real bugs. Research shows 15-30% of LLM-generated test functions lack any assertion.

## Problem

- **Claude Code, Cursor, Cline/OpenHands**: None detect hollow tests at write time.
- **Aider**: Runs tests but doesn't analyze whether they actually assert anything.
- **go vet / staticcheck**: Do not flag test functions without assertions.
- ** testify**: No built-in check for test functions that call zero assertion methods.

## Implementation

**Files**: `internal/agent/assertion_presence_check.go`, `internal/agent/assertion_presence_check_test.go`

**Check #38** in the `checkWriteIntegrity` pipeline (`write_integrity.go`).

### How it works

1. **Scope**: Only runs on `*_test.go` files.
2. **AST analysis**: Parses Go test files to find `Test*` functions (excluding `TestMain`).
3. **Assertion detection**: Counts calls that look like assertions:
   - `t.Error`, `t.Errorf`, `t.Fatal`, `t.Fatalf`, `t.Fail`, `t.FailNow`
   - `require.*` (testify), `assert.*` (testify), `quick.*`, `check.*`
4. **Delta-aware**: Only flags:
   - New test functions with zero assertions
   - Test functions whose assertion count dropped to zero (regression)
   - Pre-existing hollow tests are NOT flagged
5. **Sub-test aware**: Assertions inside `t.Run` closures are counted.

### False positive mitigation

- `TestMain` is excluded (setup/teardown, not a test)
- Benchmark functions excluded (performance measurement, not correctness)
- Only flags when assertion count is exactly zero (not merely low)
- Pre-existing hollow tests are not flagged

### Test coverage

12 test cases covering:
- Non-test files (no warning)
- Hollow test functions (warning)
- Tests with `t.Errorf`, `t.Fatal` (no warning)
- Tests with `require.Equal`, `assert.True` (no warning)
- `TestMain` exclusion
- Pre-existing hollow tests (not flagged)
- Assertion removal regression detection
- Multiple hollow tests in one file
- Mixed valid + hollow tests
- Sub-test with assertions inside `t.Run`
