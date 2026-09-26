# Cross-Session Content Search

## Motivation

The `/sessions` panel lists saved sessions by title and preview snippet, but it only
filters by metadata. Users with many sessions cannot find a past conversation by its
**content** — they must manually open each session to check if it contains the code,
error, or topic they remember.

Competitors (Claude Code `--resume --query`, Cursor's chat history search, GitHub
Copilot's conversation search) all offer full-text search across past conversations.

## Design

### Search Scope

Search scans the JSONL message records of all sessions in the store directory.
It matches case-insensitively on message content (both user and assistant messages).

### Query Semantics (r111)

The query is tokenized before matching:

- **Bare words** are whitespace-separated and matched as case-insensitive substrings.
- **Double-quoted segments** stay one phrase term: `/search oauth "rate limit"`
  requires both `oauth` and the exact phrase `rate limit`.
- **AND semantics**: every term must appear in the same message text block.
  Multi-keyword queries such as `oauth token refresh` no longer require the
  terms to appear as one consecutive substring.
- Unbalanced quotes treat the remainder as a phrase; queries that reduce to
  no terms (empty, quotes-only) return no results.
- Single-term queries behave exactly as before this change.

### Implementation

**`internal/session/search.go`** — `SearchSessions(query string, limit int)` on `JSONLStore`:

1. Tokenize the query (`tokenizeQuery`) into lowercased terms (words + quoted phrases).
2. Enumerate all session files via `loadIndex()`.
3. For each session, stream-read the JSONL file line-by-line with a
   line-length-bounded reader (avoids loading entire sessions into memory).
4. For each record, extract message content from `Message.Content` and require an
   all-terms match (`matchAllTerms`) inside one text block.
5. On match, extract a snippet (~200 chars centered on the earliest term hit) and record:
   - `SessionID` — for resume action
   - `Title` — from the session index
   - `Role` — user/assistant
   - `Snippet` — context around the match
   - `Timestamp` — from the record
6. Sort results by timestamp descending.
7. Cap at `limit` (default 100) to bound response size.

### TUI Integration

**`/search <query>`** slash command:
- Opens the inspector panel in `inspectorPanelSearch` mode.
- Search runs asynchronously (via `m.program.Send(inspectorItemsLoadedMsg)`) to avoid
  blocking the UI on large session stores.
- Results render as inspector panel items: title, role badge, timestamp, snippet.
- `Enter` on a result resumes that session (same handler as `/sessions`).

### Data Flow

```
User types /search "auth bug"
  → commands.go routes to openSearchPanel(query)
    → goroutine: SearchSessions(query, 100)
      → scans JSONL files line-by-line
      → returns []SearchResult
    → m.program.Send(inspectorItemsLoadedMsg{items})
      → model_update.go caches items in inspectorPanel
        → inspector_panel.go renders results
          → Enter → resumeSession(item.ID)
```

### Future Enhancements

- **Regex search** — support `re:` prefix for regex patterns
- **Date filtering** — `before:`/`after:` qualifiers
- **Role filtering** — `user:`/`assistant:` prefix
- **Full-text index** — pre-built inverted index for faster search on very large stores
- **Search history** — remember recent searches
