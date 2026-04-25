# Prompt Submission Latency Analysis

From the moment the user presses Enter to when the first LLM token arrives, what happens?

---

## Full Call Chain

```
User presses Enter (TUI Update goroutine)
  │
  ├─ 1. model_update.go:528  — text = input.Value(); input.SetValue("")
  │     ~instant
  │
  ├─ 2. commands.go:127  — submitText(text, true)
  │     ├─ stripPendingImagePlaceholder(text)
  │     └─ handleCommand(text)
  │         ├─ parseShellCommand — regex check
  │         ├─ slash command check (if starts with "/")
  │         └─ **Regular message path** (commands.go:580-604):
  │
  ├─ 3. ⚠️ commands.go:590 — m.appendUserMessage(text)    ← ON TUI THREAD
  │     ├─ sessionMutex().Lock()
  │     ├─ store.AppendMessage(session, msg)               ← FILE I/O (open + append JSONL)
  │     │   ├─ os.OpenFile(session.jsonl, O_CREATE|O_WRONLY|O_APPEND)
  │     │   ├─ json.Encode(record)
  │     │   └─ session.updateIndex()                       ← may re-marshal + rewrite index
  │     └─ sessionMutex().Unlock()
  │
  ├─ 4. commands.go:604 — return tea.Batch(spinner, startAgentWithExpand(text))
  │     ~instant (returns tea.Cmd, actual work is async)
  │
  ╰─ TUI Update returns → user sees spinner

(tea.Cmd executes — goroutine A)
  │
  ├─ 5. submit.go:93-116 — startAgentWithExpand → tea.Cmd body
  │     ├─ go func() { ... }()                            ← spawns goroutine B
  │     └─ returns nil immediately
  │
(goroutine B — the actual agent runner)
  │
  ├─ 6. submit.go:103-104 — ExpandMentions(text, workDir)
  │     ├─ ParseMentions: regex to extract @path tokens
  │     ├─ For each @mention:
  │     │   ├─ os.Stat(path)
  │     │   ├─ If file: os.ReadFile(path)                 ← FILE I/O per mentioned file
  │     │   └─ If dir:  os.ReadDir(path)                  ← FILE I/O per mentioned dir
  │     └─ Returns expanded text
  │
  ├─ 7. submit.go:110 — runAgentSubmission(ctx, runID, expandedMsg, img)
  │     ├─ buildAgentSubmissionContent(text, img, false)
  │     ├─ If img != nil: activeEndpointSupportsVision()
  │     │   └─ config.ResolveActiveEndpoint()              ← config parsing
  │     └─ runAgentWithContent(ctx, runID, content)
  │
  ├─ 8. agent.go:327 — agent.RunStreamWithContent(ctx, content, onEvent)
  │     │
  │     ├─ 8a. contextManager.Add(userMessage)
  │     │   ├─ mu.Lock()
  │     │   ├─ countTokens(msg)                            ← may call provider.CountTokens (HTTP!)
  │     │   │   └─ timeout: 100ms, fallback to local estimate
  │     │   └─ mu.Unlock()
  │     │
  │     ├─ 8b. ⚠️ maybeAutoCompact(ctx, onEvent)
  │     │   ├─ Check if tokenCount >= autoCompactThreshold
  │     │   ├─ If triggered: CheckAndSummarize()
  │     │   │   ├─ Microcompact() — in-memory truncation of old tool results
  │     │   │   └─ If still over threshold: Summarize()
  │     │   │       └─ ⚠️⚠️ summarizeMessages()             ← FULL LLM CALL
  │     │   │           └─ prov.Chat(ctx, summaryMsgs, nil) — non-streaming!
  │     │   └─ If compacted significantly: maybeSaveCheckpoint()
  │     │       └─ repl.go checkpoint handler → store.AppendCheckpoint ← FILE I/O
  │     │
  │     ├─ 8c. tools.ToDefinitions()
  │     │   └─ Iterates all registered tools, builds JSON schema defs
  │     │
  │     └─ 8d. **Agent loop iteration 0** (agent.go:346-361)
  │         ├─ ctx.Err() check
  │         ├─ injectPendingInterruptions()
  │         ├─ maybeAutoCompact (again — but usually skipped)
  │         ├─ ⚠️ contextManager.Messages()
  │         │   └─ mu.Lock() → deep copy all messages → mu.Unlock()
  │         │
  │         └─ 9. streamChatResponse(ctx, msgs, toolDefs, onEvent)
  │             │
  │             └─ 9a. provider.ChatStream(ctx, msgs, toolDefs)
  │                 ├─ openai: convertMessages()            ← JSON serialization of all msgs
  │                 ├─ convertTools()                       ← JSON serialization of tool defs
  │                 └─ ⚠️⚠️⚠️ client.CreateChatCompletionStream()
  │                     └─ HTTP POST to LLM API             ← NETWORK CALL (first byte latency)
  │
  ╰─ Stream events start flowing → onEvent → program.Send → TUI renders first token
```

---

## First Run vs Subsequent Runs

### First run (empty/new session)

| Step | What happens | Latency |
|------|-------------|---------|
| 1-4 | TUI: input + appendUserMessage + spinner start | ~1-5ms (local) |
| 5-6 | ExpandMentions (usually no mentions on first msg) | ~0ms |
| 7 | buildAgentSubmissionContent | ~0ms |
| 8a | contextManager.Add — countTokens (usually local estimate) | ~0-1ms |
| 8b | maybeAutoCompact — **skipped** (tokens < threshold) | ~0ms |
| 8c | tools.ToDefinitions | ~0.1ms |
| 8d | Messages() — deep copy (small message list) | ~0ms |
| 9a | convertMessages — small payload | ~0ms |
| 9a | **HTTP POST to LLM** | **500ms-3s+** |

**First run total (before LLM): ~1-10ms** — effectively instant, user perceives only LLM latency.

### Subsequent runs (long conversation, many tool results)

| Step | What happens | Latency |
|------|-------------|---------|
| 1-4 | TUI: appendUserMessage + spinner | ~1-5ms |
| 6 | ExpandMentions (if @file used) | ~1-50ms per file |
| 8a | contextManager.Add — countTokens (may call API if provider set) | **0-100ms** |
| 8b | maybeAutoCompact — **MAY TRIGGER** if tokens > threshold | **0ms OR 2-10s+** |
| 8c | tools.ToDefinitions | ~0.1ms |
| 8d | Messages() — deep copy (large message list) | **0.1-5ms** |
| 9a | convertMessages — JSON serialize large history | **1-50ms** |
| 9a | **HTTP POST to LLM** (large payload → slow upload) | **1-10s+** |

---

## Hotspots — Potentially Slow Operations

### 🔴 1. Auto-Compact Summarization — THE BIG ONE
**File:** `internal/agent/agent_compact.go:83-125` → `internal/context/manager.go:308-348` → `manager.go:593-632`

```
maybeAutoCompact → CheckAndSummarize → Summarize → summarizeMessages → prov.Chat()
```

- **When:** Token count exceeds `autoCompactThreshold` (typically ~70% of context window)
- **What:** Makes a **full synchronous LLM call** (non-streaming `prov.Chat`) to summarize conversation history
- **Duration:** **2-30 seconds** depending on history size and LLM speed
- **Impact:** User sees "thinking" spinner but no tokens are being generated. Appears as if the app froze.
- **First run:** Never triggers (empty context)
- **Subsequent:** Triggers every N turns once context fills up

### 🟠 2. Token Counting via Provider API
**File:** `internal/context/manager.go:372-382`

```go
func (m *Manager) countTokens(msg provider.Message) int {
    if m.provider != nil {
        ctx, cancel := context.WithTimeout(context.Background(), 100ms)
        if n, err := m.provider.CountTokens(ctx, []provider.Message{msg}); err == nil && n > 0 {
            return n
        }
    }
    return estimateTokens(msg) // local fallback
}
```

- **When:** Every `contextManager.Add()` call (once per user message, once per assistant message, once per tool result)
- **What:** May make an HTTP call to the provider's token counting endpoint
- **Duration:** 0ms (local estimate) or **up to 100ms** (API call, then fallback)
- **Impact:** If API is unreachable, 100ms timeout × number of `Add()` calls in the loop

### 🟠 3. @Mention File Expansion
**File:** `internal/tui/completion.go:95-144`

- **When:** User types `@path/to/file` in their message
- **What:** Reads each mentioned file's content or directory listing
- **Duration:** **1-50ms per file**, more for large files (capped at `maxMentionFileSize`)
- **Impact:** Runs in background goroutine (good), but delays agent start
- **Mitigation:** Already runs in goroutine B, not on TUI thread

### 🟡 4. appendUserMessage (File I/O on TUI thread)
**File:** `internal/tui/submit.go:19-43`

- **When:** Every message submission, on the TUI thread
- **What:** Opens JSONL file, appends JSON record, updates index
- **Duration:** **0.5-5ms** typically, **10-50ms** on slow disks (NFS, HDD under load)
- **Impact:** Brief UI freeze on every Enter press

### 🟡 5. Messages() Deep Copy
**File:** `internal/context/manager.go:148-154`

```go
func (m *Manager) Messages() []provider.Message {
    m.mu.Lock()
    defer m.mu.Unlock()
    out := make([]provider.Message, len(m.messages))
    copy(out, m.messages)
    return out
}
```

- **When:** Every agent loop iteration (before each LLM call)
- **What:** Deep copies entire message history under lock
- **Duration:** **0.01ms** (10 messages) to **5ms** (1000+ messages with large tool results)
- **Impact:** Minor, but scales linearly with conversation length

### 🟡 6. convertMessages (JSON Serialization)
**File:** `internal/provider/openai.go:403+`

- **When:** Every LLM call
- **What:** Converts all messages to provider-specific format + JSON serialization
- **Duration:** **0.1-50ms** depending on message count and content size
- **Impact:** Linear with message count; large tool results are expensive to serialize

### 🟢 7. tools.ToDefinitions
**File:** `internal/tool/tool.go:99-110`

- **When:** Once per `RunStreamWithContent` call, plus each loop iteration
- **What:** Iterates all registered tools and builds schema definitions
- **Duration:** **~0.1ms** (fixed, typically 20-30 tools)

---

## Visual Timeline

### First message (empty session)
```
TUI thread:  [input] [appendMsg~2ms] [spinner] ───────────────────────────────────►
                                                                            ↑
Agent goroutine:                    [expand~0ms] [add+count~1ms] [skip compact]
                                    [toDefs~0.1ms] [copy msgs~0ms] [serialize~0ms]
                                                                            ↓
Network:                                                            [POST → LLM API]
                                                                    ←──── 1-3s ────→
                                                                                     ↑
                                                                              first token arrives
```

### 10th message (context ~60% full)
```
TUI thread:  [input] [appendMsg~2ms] [spinner] ───────────────────────────────────►
                                                                            ↑
Agent goroutine:                    [expand~0ms] [add+count~1ms] [skip compact]
                                    [toDefs] [copy msgs~0.5ms] [serialize~5ms]
                                                                            ↓
Network:                                                            [POST → LLM API]
                                                                    ←── 1-5s ──→
                                                                                  first token
```

### 20th message (context ~75%, triggers auto-compact)
```
TUI thread:  [input] [appendMsg~2ms] [spinner] ─────────────────────────────────────────────────────────────────►
                                                                                                                ↑
Agent goroutine:                    [expand] [add+count] [⚠️ AUTO-COMPACT: summarize → LLM call 2-10s]
                                    [checkpoint save~5ms] [toDefs] [copy msgs~0.5ms] [serialize~2ms]
                                                                                                                ↓
Network:                                                                                               [POST → LLM API]
                                                                                                        ←── 1-3s ──→
                                                                                                                    first token
```

### With @mention
```
TUI thread:  [input] [appendMsg~2ms] [spinner] ───────────────────────────────────────────►
                                                                                    ↑
Agent goroutine:                    [⚠️ ExpandMentions: read 3 files ~10-50ms]
                                    [add+count] [skip compact] [toDefs] [serialize]
                                                                                    ↓
Network:                                                                    [POST → LLM API]
                                                                            ←── 1-3s ──→
```

---

## Key Differences: First Run vs Subsequent

| Aspect | First Run | Subsequent Runs |
|--------|-----------|-----------------|
| appendUserMessage | ~2ms (create new JSONL) | ~2ms (append to existing) |
| ExpandMentions | Usually no mentions | May have @files |
| contextManager.Add | ~0ms (local estimate) | ~0ms (local estimate) or 0-100ms (API) |
| maybeAutoCompact | **Always skipped** (empty) | **May trigger full LLM call** (2-30s) |
| Messages() copy | ~0ms (1-2 msgs) | ~0.1-5ms (grows linearly) |
| convertMessages | ~0ms (tiny payload) | ~1-50ms (grows linearly) |
| HTTP POST size | ~1-2KB | **10-500KB+** |
| First byte latency | 0.5-2s | **1-10s+** (large payloads) |

---

## Summary of Fix Opportunities

| Priority | Hotspot | Fix | Expected Improvement |
|----------|---------|-----|---------------------|
| P0 | Auto-compact summarization blocking user | Show "Compacting..." status before/during summarize; consider proactive compact during idle | User awareness, not faster |
| P1 | appendUserMessage on TUI thread | Move to async tea.Cmd or background goroutine | Eliminate ~2-5ms TUI freeze |
| P2 | Token counting API calls | Always use local estimate; skip provider API | Eliminate 0-100ms stalls per Add() |
| P2 | Messages() deep copy every iteration | Cache the copy, invalidate on Add() only | Reduce per-iteration overhead |
| P3 | Large payload upload to LLM | Compress or truncate old messages before send | Reduce first-byte latency |
