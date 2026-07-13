# Message ID + Checkpoint Redesign Implementation Plan

## Problem
- `provider.Message` has no unique ID — all dedup/positioning logic uses fragile counting (OrigLen) or markers
- `checkpoint` stores full `[]Message` copy (duplicate of JSONL content, tens of KB per checkpoint)
- `runAdded` slice can cause JSONL duplication if not cleared
- precompact apply fails when messages are added/removed during goroutine execution

## Design

### ID Strategy
- `Message.ID` — `msg_` + ULID (26 chars, time-sortable), generated in `Manager.Add()` when `ID == ""`
- Only `message` and `checkpoint` record types need IDs
- `usage`/`metric` keep TurnIndex (sufficient, no dedup needed)
- `cost` stays as-is (aggregate, last-wins)
- `tunnel_event` keeps EventID/StreamID (independent system)
- `meta` stays as-is (session-level)

### Checkpoint Redesign
**Current**: `checkpoint_messages: []Message` (full context snapshot, ~50KB)
**New**: `checkpoint_summary_msg_id: "msg_01HX..."` (just an ID, ~30 bytes)

Checkpoint only records the ID of the summary message. Restore scans JSONL from that ID forward.

### Summary Writes to JSONL
Currently compaction summary replaces `m.messages` directly, NOT written to JSONL.
**Change**: summary goes through `Add()` → gets ID → written to JSONL as `type:"message"`.

### Snapshot Still Needed
`CompactSnapshot` copies message CONTENT for LLM summarization in background goroutine. Cannot read live `m.messages` during compaction. But `OrigLen` replaced by `LastMsgID`.

### ApplyCompactResult
Find `snapshot.LastMsgID` in `m.messages` → `extra = messages[markerIdx+1:]`. No fallback needed after migration (all messages have IDs).

### Restore Logic
1. Scan JSONL, find last `type:"checkpoint"` → get `summary_msg_id`
2. Find `summary_msg_id` position in JSONL message records
3. `ContextMessages = JSONL[summary_msg_id_position..]` (all messages from summary onward)
4. No checkpoint → last 200 message records
5. System prompt rebuilt by system prompt builder (not in JSONL)

### Migration (program-internal, startup)
1. Scan each `.jsonl` session file
2. `type:"message"` with `id == ""` → generate ULID, update record
3. `type:"checkpoint"` with `checkpoint_messages` (old format):
   - If summary message found in checkpoint_messages → write it to JSONL as `type:"message"` with new ID → set `checkpoint_summary_msg_id`
   - If no summary found (truncation-only checkpoint) → **discard checkpoint**
4. Write back to temp file, replace original
5. After migration: all messages have IDs, all checkpoints are new format, no fallback needed

## Implementation Phases

### P1: Message.ID + Add() generation + Migration
**Files**:
- `internal/provider/provider.go` — add `ID string` field to `Message`
- `internal/context/manager.go` — `Add()` generates ULID when ID empty
- `internal/session/store.go` — migration logic on session load
- `internal/session/migrate.go` (new) — `MigrateSessionFile(path)` function

**Acceptance**: build passes, existing tests pass, new session files have message IDs

### P2: Checkpoint redesign
**Files**:
- `internal/session/store.go` — `jsonlRecord.CheckpointSummaryMsgID` field, remove `CheckpointMessages`
- `internal/context/manager.go` — `Summarize()` writes summary via `Add()` 
- `internal/agent/agent_compact.go` — `maybeSaveCheckpoint()` saves only `summary_msg_id`
- `internal/tui/repl.go` — checkpoint callback updated

**Acceptance**: checkpoint records are ~30 bytes instead of ~50KB

### P3: Restore logic
**Files**:
- `internal/session/store.go` — `loadSession()` uses `summary_msg_id` to locate start position

**Acceptance**: session restore works correctly with new checkpoint format

### P4: Precompact apply + remove marker
**Files**:
- `internal/context/manager.go` — `CompactSnapshot` uses `LastMsgID`, `ApplyCompactResult` uses ID lookup
- `internal/agent/agent_precompact.go` — remove all marker code (InsertCompactionMarker/RemoveCompactionMarker)
- `internal/context/manager.go` — remove marker sentinel/filter code

**Acceptance**: precompact apply 100% success rate (no discard due to message count mismatch)

### P5: runAdded dedup + persist
**Files**:
- `internal/context/manager.go` — `runAdded` → `runStartIDs map[string]bool`
- `internal/tui/session_persist.go` — persist by ID dedup

**Acceptance**: no JSONL duplication even if StartRunTracking not called

### P6: Desktop SessionMessage ID
**Files**:
- `desktop/wailskit/chat.go` — `SessionMessage.ID` field
- `desktop/ggcode-desktop-wails/frontend/src/types.ts` — ID in frontend type

**Acceptance**: frontend can use message ID as React key

## Confirmed Decisions
1. No TurnID — turn concept too fuzzy in code, not worth complexity
2. No fallback for missing IDs after migration — migration is mandatory
3. Old checkpoints without summary → discarded during migration
4. System prompt always rebuilt at runtime, never in JSONL
5. Snapshot still needed for background summarize (content copy)
6. tunnel/IM/cost/usage/metric not changed
