# Affected-Test Detection (Test Impact Analysis)

## Trend
Test-impact analysis / "affected" scope detection is a core engineering trend
(Nx `--affected`, Turborepo scope hoisting, Vitest-affected, Jest
`--findRelatedTests`, Aider's edit-then-test loop). The idea: after editing a
file, run only the tests/builds reachable from that change rather than the whole
project suite, giving far faster feedback inside the inner dev loop.

## Gap (ggcode before this change)
The post-edit verification hint (`internal/agent/verify_hint.go`) nudged the
agent to run the **full** project suite (`make verify-ci`, `go build ./...`,
etc.) every few edits. For a large monorepo this is slow and discourages the
"verify early, verify often" loop. There was no path/affected-package scoping.

## Competitors
- **Aider**: runs the user-specified test command after edits; no automatic
  package scoping, but the workflow centers on edit→test.
- **Claude Code / Cursor / Cline**: the agent itself chooses what to test;
  quality varies. No deterministic affected-package hint.
- **CI tooling (Nx/Turborepo/Jest)**: deterministic affected detection, but
  only in CI, not in the agent's mid-session loop.

## Implementation
Added `targetedVerifyCommand` / `goTargetedTestCommand` in `verify_hint.go`.
For a Go edit it locates the enclosing module (walks up for `go.mod`) and emits
`go test ./<pkg-dir>/` scoped to the file's package. The hint now surfaces
**both** the fast targeted command and the full-suite fallback:

> Run `go test ./internal/agent/` for a fast check of the package you just
> edited (`foo.go`), or `make verify-ci` for the full suite before finishing.

Non-Go files fall back to the full-suite command only, preserving prior
behavior. When no module root is found or the file escapes the module, the
targeted command is omitted so existing behavior is unchanged.

## Scope / future work
- Only Go modules are scoped today. Other ecosystems (Rust crate dirs, JS
  package roots) can be added with the same `targetedVerifyCommand` dispatcher.
- Build tags (e.g. `-tags goolm`) are intentionally omitted from the fast hint;
  the full suite remains the authoritative check.
