# Session Recall

## What It Does

`session_recall` lets the agent keyword-search the full transcript history of
its own and sibling sessions in this workspace — including messages and tool
results that were compacted out of the live context long ago.

Long sessions get compressed; after enough rounds, details like an early user
instruction, a decision, or a file dump from a `read_file` call may only exist
in the raw session JSONL files. This tool reaches that history without
re-reading and re-parsing multi-hundred-megabyte files on every query.

## Usage

```
session_recall(query, session_id?, role?, limit)
```

| Parameter | Description |
|-----------|-------------|
| `query` | Space-separated keywords. All terms must appear in the same message (AND semantics). |
| `session_id` | Restrict the search to one session. Omit to search all sessions in the workspace. |
| `role` | Filter by message role: `user`, `assistant`, or `tool`. |
| `limit` | Max results (default 10, max 50). Results are newest-first with role and timestamp. |

## How It Works

- A **trigram inverted index** is maintained per session file (under
  `~/.ggcode/sessions/idx/<session-id>.idx`). Postings store byte offsets into
  the append-only JSONL, so a query seeks straight to matching lines instead
  of scanning the file.
- **Ownership**: each ggcode instance writes the index for its own live
  session only (incrementally, as messages are appended). Sibling sessions'
  indexes are written by their own instances.
- **Refresh**: the own index tails new messages on each query; sibling
  indexes are checked every 30 minutes and reloaded when they change.
- **Size gates**: if the own index exceeds 500 MB, sibling indexes are not
  loaded (self-only mode). Any sibling index larger than 200 MB is skipped.
- The first query against a large never-indexed history pays a one-time
  index build; queries afterwards are posting-list lookups.

## What Gets Indexed

User and assistant text blocks plus tool-result text — the content most
likely to hold recoverable facts after compaction.
