# Copilot-Style TUI Message Display Design

## Goal

Replace the current raw-text echo display with a Copilot CLI-inspired conversation layout:

- User input shown with `❯ ` prefix (shell-like)
- Assistant text shown with `● ` prefix  
- Tool calls shown as indented tree blocks (`│`, `└`)
- No "You:" or "Assistant:" labels anywhere

## Current Behavior (app.go)

1. User presses Enter → `m.output.WriteString(text)` echoes raw input + `\n\n`
2. `streamStartPos = m.output.Len()` marks where assistant stream begins
3. `streamMsg` events → written to `m.output` directly AND to `streamBuffer`
4. `doneMsg` → truncate back to `streamStartPos`, render markdown from `streamBuffer`, write back
5. Tool calls → `toolStatusMsg` with `Running:true` starts spinner, `Running:false` writes `[done] toolName`

## Proposed Changes

### 1. User Input Display

**File:** `internal/tui/app.go` — `handleCommand()` ~line 951

Replace:
```go
m.output.WriteString(text)
m.output.WriteString("\n\n")
```

With:
```go
m.output.WriteString("❯ ")
m.output.WriteString(text)
m.output.WriteString("\n")
```

**Also update** the input prompt style (line 208) — change `ti.Prompt = "> "` to `ti.Prompt = "❯ "` for visual consistency.

### 2. Assistant Response Prefix

**File:** `internal/tui/app.go` — `Update()` `streamMsg` case ~line 499

When the first stream token arrives and `streamBuffer` was just created, prepend a `● ` prefix to the assistant's output.

Approach: Add a `streamPrefixWritten bool` field to `Model`. On first `streamMsg`, write `"● "` before the stream content. On `doneMsg`, when replacing stream buffer with rendered markdown, include the `● ` prefix in the rendered output.

Concretely:
- In `startAgent()`, set `m.streamPrefixWritten = false`
- In `streamMsg` handler: if `!m.streamPrefixWritten`, write `"● "` to `m.output` and `m.streamBuffer`, set `m.streamPrefixWritten = true`
- In `doneMsg` handler: prepend `"● "` to the rendered markdown string before writing to `m.output`

### 3. Tool Call Display (Tree-Style)

**File:** `internal/tui/spinner.go` — `FormatToolStatus()`

Replace the current `[done] toolName` / `[error] toolName` format with:

```
│ toolName(arg1, arg2)  (N lines read)
│   first line of result
│   second line
└ done (0.3s)
```

**New fields needed on `ToolStatusMsg`:**
- `Args string` — tool arguments summary (truncated)
- `Duration time.Duration` — how long the tool took
- `ResultLines int` — number of lines in result (for summary)

**Implementation:**
- `Running:true` → write `"│ " + toolName + "(" + truncatedArgs + ")"` to output (replaces spinner start)
- `Running:false` → write result lines with `"│   "` prefix, then `"└ done (Xs)"` footer

**File:** `internal/tui/app.go` — `Update()` `toolStatusMsg` case ~line 574

When `Running:true`:
- Stop current spinner (if any)
- Write the tool call header line to `m.output`
- Adjust `streamStartPos` to account for tool output inserted between assistant text and next stream chunk

When `Running:false`:
- Write result tree to `m.output`
- Write the closing `└ done` line

### 4. Separator Between Turns

Add a blank line between conversation turns (after assistant's done + before next user input).

Current `doneMsg` already writes `\n` at the end — keep this behavior.

### 5. Stream Buffer Handling with Tool Calls

The current design truncates to `streamStartPos` on `doneMsg` and replaces with rendered markdown. With tool calls interleaved, this gets more complex.

**Simpler approach:** Don't truncate/replace anymore. Instead:
- Stream text tokens → write to `m.output` as-is (with `● ` prefix on first token)
- On `doneMsg` → DON'T truncate. Just mark loading=false. The raw stream text stays in output.
- **Problem:** No markdown rendering at end of stream.

**Better approach:** Keep the truncation pattern but account for tool output:
- Track `assistantStreamStart` — position where current assistant text block starts
- When tool call starts → flush current stream buffer (render markdown, write to output, update `streamStartPos`)
- When tool call ends → write tool result tree, set new `streamStartPos`
- When `doneMsg` fires → render remaining stream buffer as before

### Model Changes

```go
type Model struct {
    // ... existing fields ...
    streamPrefixWritten bool   // NEW: whether ● prefix was written for current stream
    assistantStreamStart int   // NEW: position where current assistant text stream starts
    lastToolEndPos       int   // NEW: position after last tool result
}
```

### Message Flow (Final Design)

```
User types: "check the weather"
→ Output: "❯ check the weather\n"

Agent starts thinking, first text token arrives:
→ Output: "❯ check the weather\n● It looks like today's weather..."

Agent calls a tool (run_command):
→ Flush stream buffer → render markdown → write to output
→ Output: "❯ check the weather\n● Let me check...\n\n│ run_command(curl wttr.in)\n│   ...result...\n└ done (1.2s)\n\n"
→ Reset stream buffer

Agent continues with more text:
→ Output: "...│ run_command...\n└ done\n\n● The weather in Shanghai is 22°C..."

Agent finishes:
→ doneMsg → flush remaining stream buffer → render markdown → write
→ Output: "...● The weather in Shanghai is **22°C** with clear skies.\n\n"
```

## Files to Modify

| File | Change |
|------|--------|
| `internal/tui/app.go` | User input `❯` prefix; `streamPrefixWritten` logic; tool call output integration; `doneMsg` flush logic |
| `internal/tui/spinner.go` | New `FormatToolStatus()` tree-style format; add `Args`/`Duration`/`ResultLines` to `ToolStatusMsg` |
| `internal/tui/app.go` (styles) | Add `bullet` style for `● ` prefix (dim/muted color) |
| `internal/provider/provider.go` | No changes needed — existing `StreamEvent` types sufficient |

## Edge Cases

1. **Multiple tool calls in sequence:** Each gets its own tree block, separated by blank lines
2. **Tool call error:** Use `└ error` instead of `└ done`, red color
3. **Empty assistant response:** Just the `● ` prefix with nothing after, then blank line
4. **Stream interrupted (Ctrl+C):** Flush partial stream buffer as-is (no markdown render)
5. **Image attached:** Show as `● [image: filename.png]` inline

## Backward Compatibility

- Session export (`/export`) and session files are unaffected — they store raw messages, not rendered output
- Pipe mode (`-p`) is unaffected — it doesn't use the TUI
- The `styles.user` and `styles.assistant` styles still exist but won't be used for conversation turns (can keep for other UI elements)
