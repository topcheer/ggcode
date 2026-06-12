# Multi-file read/edit tool design

## Summary

Add two new built-in tools:

- `multi_file_read` for batching reads across multiple existing files in one tool call
- `multi_file_edit` for batching edits across multiple existing files in one tool call

The first version is intentionally narrow:

- Only works on existing files.
- Reuses current `read_file`, `edit_file`, and `multi_edit_file` semantics as much as possible.
- Uses grouped `files[]` inputs for both read and edit tools.
- Defaults to **atomic** cross-file behavior for easier LLM recovery.
- Optionally supports `mode: "partial_success"` for callers that prefer best-effort writes.

This keeps the tools easy for weaker models to use while avoiding a brand-new matching model.

## Goals

- Reduce round trips when one task requires coordinated edits in several files.
- Reuse the already-hardened matching behavior from `edit_file` / `multi_edit_file`, including:
  - read_file numbered-line anchors
  - wrapper-line stripping
  - indentation normalization
  - CRLF tolerance
  - leading-indent shift fallback
- Preserve the same line-numbered read format that powers the current edit workflow.
- Return structured per-file results so the agent can retry only failures.
- Keep the tool request small and predictable enough for weaker models to call reliably.
- Provide a complete multi-file read + write loop rather than only optimizing the write side.

## Non-goals

- Creating new files.
- Overwriting files wholesale like `write_file`.
- Replacing `read_file` / `edit_file` for single-file use.
- Supporting repeated `path` entries in one call.
- Changing single-file edit semantics.

## End-to-end workflow

The intended LLM workflow is:

1. Use `multi_file_read` to fetch the relevant files or file ranges in one round trip.
2. Inspect the numbered output and decide what to change.
3. Use `multi_file_edit` with numbered `old_text` anchors copied from `multi_file_read`.
4. If edit planning fails, re-read only the failed files or narrower ranges and retry.

The two tools are designed as a pair. `multi_file_read` optimizes context collection; `multi_file_edit` optimizes coordinated writes.

## Protocol clarifications

These points must be fixed in the design so implementation and agent behavior do not drift:

- `multi_file_read` and `multi_file_edit` both require absolute paths.
- Paths must be normalized with `filepath.Clean` before duplicate detection, sandbox checks, reads, writes, and reporting.
- Duplicate-path checks operate on the cleaned absolute path string.
- v1 does not attempt symlink canonicalization beyond `filepath.Clean`; callers should not provide the same target through multiple symlink aliases.
- `multi_file_edit` only supports plain text files that can be written back directly. It does not support editing extracted document formats such as PDF, DOCX, or EPUB.
- `multi_file_read` may support text files plus extracted document text, but only its plain text output is valid input to `multi_file_edit`.
- To preserve workflow parity, `multi_file_edit` matching must auto-strip `multi_file_read` wrapper lines if they are accidentally pasted into `old_text` or `new_text`. This includes:
  - `[multi_file_read summary] ...`
  - `=== FILE: ... ===`
  - `=== ERROR: ... ===`
  - `[end file]`
  - `[end error]`
  - `[skipped: ...]`

## Proposed `multi_file_read` shape

```json
{
  "files": [
    {
      "path": "/repo/a.go",
      "offset": 1,
      "limit": 120
    },
    {
      "path": "/repo/b.go",
      "offset": 40,
      "limit": 80
    }
  ],
  "description": "Optional UI label"
}
```

Rules:

- `files[].path` must be unique within one call.
- `offset` and `limit` follow the same semantics as `read_file`.
- Omitted `offset` means start from the beginning.
- Omitted `limit` means use the multi-file default per-file cap rather than the larger single-file cap.
- v1 should focus on text files and extracted document text. Image/binary batching is out of scope.

## Proposed `multi_file_edit` shape

```json
{
  "mode": "atomic",
  "files": [
    {
      "path": "/repo/a.go",
      "edits": [
        {
          "old_text": "     12\tfoo()",
          "new_text": "     12\tbar()"
        }
      ]
    },
    {
      "path": "/repo/b.go",
      "edits": [
        {
          "old_text": "x := 1",
          "new_text": "x := 2"
        }
      ]
    }
  ],
  "description": "Optional UI label"
}
```

Rules:

- `files[].path` must be unique within one call.
- Each file's `edits` behaves exactly like `multi_edit_file`.
- If the same file needs several edits, they must be grouped into one `files[i].edits`.
- `mode` defaults to `"atomic"` when omitted.
- `mode: "partial_success"` is opt-in and should be used only when the caller is prepared to reconcile mixed file state.

## Limits

The tools should be optimized for small, coordinated multi-file changes rather than giant repository-wide rewrites.

### `multi_file_read` limits

Recommended v1 limits:

- at most 20 files per call
- default per-file cap: 200 lines when `limit` is omitted
- max explicit per-file `limit`: 500 lines
- max combined output: 4000 lines or 300 KB, whichever comes first

Static request-limit violations should fail fast and tell the model to split the read into smaller batches or narrower ranges.

This is intentionally stricter than `read_file`. The point of `multi_file_read` is efficient context gathering, not dumping large files wholesale.

### `multi_file_edit` limits

Recommended v1 limits:

- at most 10 files per call
- at most 20 edits per file
- at most 200 total edits per call
- at most 200 KB combined `old_text` + `new_text` payload

Requests beyond those limits should fail fast with a clear message telling the model to split the batch.

## Execution model

Implementation should be split into three layers.

### 1. Shared single-file reader

Extract the core reading logic from `read_file` into a shared helper that:

- validates sandbox access
- reads the file
- preserves the current line-numbered output style
- preserves metadata hints such as indent or encoding headers
- applies the same range logic as `read_file`
- returns either:
  - a formatted read block
  - or a per-file read error

This helper must be reused by:

- `read_file`
- `multi_file_read`

That keeps output shape consistent so the model can copy numbered lines from either tool into edit calls.

### 2. Shared single-file planner

Extract the core planning logic from `multi_edit_file` into a shared helper that:

- reads original content
- resolves every edit against that original content
- validates uniqueness and overlap
- computes the final new content
- returns either:
  - a write plan (`path`, `new_content`, `applied_edit_count`)
  - or a structured failure

This helper must be reused by:

- `multi_edit_file`
- `multi_file_edit`

That keeps matching behavior consistent and avoids future drift.

### 3. Multi-file orchestrators

#### `multi_file_read`

`multi_file_read` should:

1. Validate top-level input before reading:
   - non-empty `files`
   - unique `path`
   - request stays within batch limits
2. For each file:
   - run the shared single-file reader
   - append either a success block or an error block
3. If the runtime combined output limit is reached:
   - stop appending further file bodies
   - append explicit skipped markers for the remaining paths
   - append a clear split-batch notice in the summary

Unlike `multi_file_edit`, `multi_file_read` should always be best-effort per file. Read failures in one path should not block the others, because partial read success is still useful for planning.

#### `multi_file_edit`

`multi_file_edit` should:

1. Validate top-level input before any write:
   - non-empty `files`
   - unique `path`
   - no empty `old_text`
   - sandbox permission for every path
   - request stays within batch limits
2. For each file:
   - read file
   - run the shared planner
   - store either a pending write or a failure result
3. If `mode == "atomic"`:
   - abort all writes if any file planning step failed
   - return all per-file failures plus any planned successes as skipped
4. If `mode == "partial_success"`:
   - persist only the successful pending writes
   - return a mixed result covering both successes and failures

This default matters for LLM efficiency. Atomic mode is easier for models to recover from because the workspace stays in one consistent pre-write state after any failure.

## Result shape

### `multi_file_read`

`multi_file_read` should optimize for readability, not JSON structure. The model needs to consume source text directly, and JSON escaping would make that harder.

Suggested `Content` format:

```text
[multi_file_read summary] requested=3 succeeded=2 failed=1

=== FILE: /repo/a.go ===
[indent: tab]
     1	package main
     2	
     3	func a() {}
[end file]

=== FILE: /repo/b.go ===
[indent: 2 spaces]
    40	foo: bar
    41	baz: qux
[File truncated: showing lines 40-41 of 120. Use multi_file_read or read_file with a narrower range for more.]
[end file]

=== ERROR: /repo/missing.go ===
error reading file: open /repo/missing.go: no such file or directory
[end error]

=== FILE: /repo/too-many.go ===
[skipped: combined output limit reached; split into a smaller batch]
[end file]
```

Design constraints:

- keep the existing numbered-line format unchanged inside each file block
- use deterministic file/error delimiters
- preserve current metadata headers when available
- keep output order identical to input order
- include explicit skipped markers when later files were omitted due to output budget

This makes the output easy for the LLM to read and easy to copy from into `multi_file_edit`.

### `multi_file_edit`

The current tool framework returns `Result.Content string`, not a native structured payload. So the tool should return a stable JSON string in `Content`, with a short human-readable summary duplicated inside that JSON.

Suggested `Content` payload:

```json
{
  "summary": "Requested 5 files: 0 written, 1 failed, 4 skipped by atomic mode",
  "mode": "atomic",
  "applied": false,
  "planned_files": 4,
  "written_files": 0,
  "failed_files": 1,
  "skipped_files": 4,
  "written_paths": [],
  "failed_paths": ["/repo/b.go"],
  "skipped_paths": ["/repo/a.go", "/repo/c.go", "/repo/d.go", "/repo/e.go"],
  "results": [
    {
      "path": "/repo/a.go",
      "status": "planned",
      "applied_edit_count": 2
    },
    {
      "path": "/repo/b.go",
      "status": "error",
      "error": "edits[1]: old_text not found in file"
    }
  ]
}
```

`results[]` order should match input order.

Status meanings:

- `success`: file was written
- `planned`: file would have succeeded, but atomic mode skipped writes after another failure
- `skipped`: file was not attempted or not written because the tool stopped before execution
- `error`: file planning or writing failed

This JSON-in-`Content` approach gives the LLM machine-stable fields without requiring a change to the current tool result contract.

### `IsError` semantics

`Result.IsError` must be defined explicitly:

- `multi_file_read`
  - `IsError = true` only for top-level malformed requests that produce no useful per-file output, such as invalid JSON, duplicate cleaned paths, or static batch-limit violations.
  - `IsError = false` for mixed read outcomes, including when some files fail or some later files are skipped due to runtime output limits.
- `multi_file_edit`
  - `IsError = false` only when every requested file is written successfully.
  - `IsError = true` in atomic mode if any file fails and no writes are applied.
  - `IsError = true` in `partial_success` mode if any requested file fails, even if some files were already written.

This preserves useful payloads while still signaling to the agent that user-visible recovery is required.

## Error handling

### `multi_file_read`

Per-file read failures should be reported inline in the output and should reuse existing `read_file` style error text where possible, such as:

- path not allowed by sandbox policy
- error accessing file
- error reading file
- file too large
- unsupported extracted content failure

Top-level validation should still fail fast for malformed requests, such as duplicate paths or batch-limit violations.

### `multi_file_edit`

Per-file failures should reuse current single-file diagnostics, including:

- `old_text not found`
- non-unique matches
- overlapping edits
- read/write errors
- sandbox denial

This is important because agents already learned those strings and recovery patterns.

Top-level validation failures should fail fast before any writes if the request itself is malformed, such as:

- duplicate `path`
- empty `files`
- invalid JSON shape
- unsupported `mode`
- request exceeds batch limits

## Registration and UX

- Add `MultiFileRead` and `MultiFileEdit` to `internal/tool/builtin.go`.
- Register `MultiFileRead` with the other file read/search tools and `MultiFileEdit` with the other mutating file tools so the built-in tool set remains easy to reason about.
- `multi_file_read` description should explicitly recommend:
  - requesting only the files and ranges needed for the current task
  - keeping ranges narrow on large files
  - copying numbered lines directly into `multi_file_edit`
- `multi_file_edit` description should explicitly recommend:
  - reading files first with `multi_file_read` or `read_file`
  - copying numbered lines as anchors
  - grouping all edits for the same file together
  - keeping batches small
  - relying on the default atomic mode unless mixed success is explicitly acceptable
- Parameter descriptions should mirror the proven language from `edit_file` / `multi_edit_file`.

## Agent, permission, and multi-agent integration

The design must also specify the integration-layer work needed beyond implementing the tools themselves.

### Tool registration

- Add `MultiFileRead{SandboxCheck: sandboxFor("multi_file_read")}` and `MultiFileEdit{SandboxCheck: sandboxFor("multi_file_edit")}` to `internal/tool/builtin.go`.
- Update any built-in tool documentation or release notes that enumerate file tools.
- Update config/documentation examples for `tool_permissions` so the new tool names are visible to users.

### Permission classification

The permission layer currently classifies file tools by name and extracts only a single path from tool input. That is insufficient for batched file tools.

Implementation must:

- classify `multi_file_read` as a read-only file tool in plan mode
- classify `multi_file_edit` as a write file tool
- extend the permission path extractor to understand `files[].path`
- apply sandbox/path checks to every cleaned path in the request, not just the first one

Without this, plan mode, auto mode, and bypass/autopilot sandbox enforcement will be incomplete for batched tools.

### File diff preview and checkpoints

The current agent special-cases only `edit_file` and `write_file` for diff preview and checkpoint persistence. `multi_file_edit` must not silently bypass that behavior.

Integration should therefore either:

- generalize the current single-file flow into a multi-file-aware diff/checkpoint path, or
- add a dedicated `executeMultiFileTool` path that computes diffs per file before writing

The preferred behavior is:

- show one combined confirmation flow containing per-file diffs when confirmation is enabled
- persist checkpoints for each successfully written file
- skip checkpoint writes for planned/skipped/error files

### WorkingDir and multi-agent isolation

This is the most important integration constraint:

- `multi_file_read` and `multi_file_edit` should be implemented as stateless tools with **no `WorkingDir` field**.
- They should require absolute paths and rely only on `SandboxCheck`.
- They should never set `SuggestedWorkingDir`.

Because they have no mutable `WorkingDir`, they are safe to share across agents and do not need `Clone()`.

If a future revision adds `WorkingDir` or relative-path semantics, then it MUST also:

- implement `Clone() Tool`
- be covered by `TestAllWorkingDirToolsImplementCloner`
- remain compatible with `syncToolWorkingDir`

This is required so sub-agents and worktree-based agents do not overwrite each other's effective working directory.

## Testing

Add tests for:

1. top-level validation
   - empty files array
   - duplicate path
   - non-absolute path
   - unsupported mode
   - request exceeds limits
2. multi-file read behavior
   - multiple file blocks are returned in input order
   - per-file offset/limit is respected
   - read errors are isolated to the failing path
   - combined output limit is enforced with explicit skipped markers
   - numbered lines and metadata headers match `read_file`
3. atomic default behavior
   - one file succeeds, one fails, no file is written
   - successful files are reported as `planned`
4. partial success mode
   - one file succeeds, one fails, only the successful file is written
5. result ordering
   - output order matches input order
6. matching parity
   - numbered anchor support
   - duplicate-text disambiguation
   - wrapper-line stripping
   - CRLF and indentation normalization
7. result content format
   - `multi_file_read` block delimiters are stable
   - `multi_file_edit` `Content` is valid JSON
   - `planned_files` / `written_files` / `failed_files` / `skipped_files` are internally consistent
   - `written_paths` / `failed_paths` / `skipped_paths` match actual writes
   - `IsError` matches the documented mode semantics
8. registration
    - built-in registry exposes both `multi_file_read` and `multi_file_edit`
   - permission mode classification includes both tools
9. workflow parity
   - content copied from `multi_file_read` can be used directly as anchors in `multi_file_edit`
   - `multi_file_read` wrapper lines pasted into `multi_file_edit` are ignored correctly
10. integration behavior
   - multi-file edit participates in diff preview/checkpoint flow
   - multi-agent/worktree tests confirm no working-directory cross-talk

## Future extensions

Possible later expansions, intentionally out of scope for v1:

- mixed edit/write semantics for creating new files
- multi-file image/binary reading
- directory creation
- dry-run preview mode
