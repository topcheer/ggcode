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

## 内存上界（自帽）

- 每实例**本会话索引**常驻上界默认 256MB（`GGCODE_SESSIONIDX_SELF_BUDGET` 可调，字节）；超帽后停止摄入 trigram，检索自动降级为有界源扫描（结果仍正确，代价为按查询的 I/O）。
- 跨会话/兄弟席索引由机器全局预算（默认 1GB）+ 30 分钟空闲卸载约束；宿主 socket 位于 `~/.ggcode/idxd.sock`。

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
- **Storage format (v2, grouped)**: postings are grouped per source line on
  disk — line metadata (offset/length/role/timestamp) is stored once per
  line and trigrams become dictionary IDs. Measured ~8x smaller than the
  legacy per-posting format (937MB session: 938MB → ~120MB idx). Indexes
  in a foreign (older) format are treated as not-yet-reindexed and rebuilt
  by their owner's next refresh — no migration step.
- **Dead-index cleanup**: a persisted idx past the decode-skip threshold
  (64 MB) is permanently frozen (loads capped, never rewritten); Load
  deletes it on sight. Its session still recalls via the bounded source
  scan — only derived data is reclaimed.
- **Ownership**: each ggcode instance writes the index for its own live
  session only (incrementally, as messages are appended). Sibling sessions'
  indexes are written by their own instances. When a shared host is alive,
  instances become **pure clients**: they skip the local live-session
  index entirely (the host's 30s tail owns every session, including the
  live one) and recall flows through the host socket, falling back to the
  local path whenever the host is unreachable.
- **Refresh**: the own index tails new messages on each query; sibling
  indexes are checked every 30 minutes and reloaded when they change.
- **Size gates**: if the own index exceeds 500 MB, sibling indexes are not
  loaded (self-only mode). Any sibling index larger than 200 MB is skipped.
  The host tail never builds an index for a session whose JSONL exceeds
  200 MB — recall for those serves from the bounded source scan.
- The first query against a large never-indexed history pays a one-time
  index build; queries afterwards are posting-list lookups.

## What Gets Indexed

User and assistant text blocks plus tool-result text — the content most
likely to hold recoverable facts after compaction.
