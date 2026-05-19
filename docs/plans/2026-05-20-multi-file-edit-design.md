# Multi-file edit tool design

## Summary

Add a new built-in tool, `multi_file_edit`, for batching edits across multiple existing files in one tool call.

The first version is intentionally narrow:

- Only edits existing files.
- Reuses current `edit_file` / `multi_edit_file` matching semantics.
- Uses a grouped shape: `files[].path + files[].edits`.
- Is **partially successful**, not globally atomic: one file may fail while others still persist.

This keeps the tool easy for weaker models to use while avoiding a brand-new matching model.

## Goals

- Reduce round trips when one task requires coordinated edits in several files.
- Reuse the already-hardened matching behavior from `edit_file` / `multi_edit_file`, including:
  - read_file numbered-line anchors
  - wrapper-line stripping
  - indentation normalization
  - CRLF tolerance
  - leading-indent shift fallback
- Return structured per-file results so the agent can retry only failures.

## Non-goals

- Creating new files.
- Overwriting files wholesale like `write_file`.
- Cross-file atomic rollback.
- Supporting repeated `path` entries in one call.
- Changing single-file edit semantics.

## Proposed tool shape

```json
{
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

## Execution model

Implementation should be split into two layers.

### 1. Shared single-file planner

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

### 2. Multi-file orchestrator

`multi_file_edit` should:

1. Validate top-level input before any write:
   - non-empty `files`
   - unique `path`
   - no empty `old_text`
   - sandbox permission for every path
2. For each file:
   - read file
   - run the shared planner
   - store either a pending write or a failure result
3. Persist only the successful pending writes.
4. Return a mixed result covering both successes and failures.

Because the chosen behavior is partial success, there is no global rollback across files.

## Result shape

The tool should return both a readable summary and machine-stable detail.

Suggested top-level content:

`Applied edits to 5 files: 4 succeeded, 1 failed`

Suggested structured fields:

```json
{
  "summary": "Applied edits to 5 files: 4 succeeded, 1 failed",
  "succeeded_files": 4,
  "failed_files": 1,
  "results": [
    {
      "path": "/repo/a.go",
      "status": "success",
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

## Error handling

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

## Registration and UX

- Add `MultiFileEdit` to `internal/tool/builtin.go`.
- Tool description should explicitly recommend:
  - reading files first with `read_file`
  - copying numbered lines as anchors
  - grouping all edits for the same file together
- Parameter descriptions should mirror the proven language from `edit_file` / `multi_edit_file`.

## Testing

Add tests for:

1. top-level validation
   - empty files array
   - duplicate path
2. partial success
   - one file succeeds, one fails, only the successful file is written
3. result ordering
   - output order matches input order
4. matching parity
   - numbered anchor support
   - duplicate-text disambiguation
   - wrapper-line stripping
   - CRLF and indentation normalization
5. registration
   - built-in registry exposes `multi_file_edit`

## Future extensions

Possible later expansions, intentionally out of scope for v1:

- mixed edit/write semantics for creating new files
- all-or-nothing cross-file transaction mode
- directory creation
- dry-run preview mode
