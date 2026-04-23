# Chat Rendering V2 — Design Document

## Problem Statement

### Current Issues

1. **O(N²) streaming bug**: Each chunk appends duplicate entries to chatEntries (fixed in ea6aa8f but architecture still fragile)
2. **No virtual scrolling**: `ChatEntryList.Render()` traverses ALL entries every frame — 1000 messages = 1000 renders per frame
3. **Tool calls grouped poorly**: Activity groups with 📦 headers, max 5 items visible, hidden count summary — hard to follow individual tool progress
4. **Todos/Tasks/Subagents rendered with old-style text**: Plain text lists, no structured display, no collapse/expand
5. **Daemon follow mode uses separate rendering code**: `TerminalFollowDisplay` in `internal/daemon/follow.go` has its own ANSI formatting, completely different from TUI
6. **IM tool formatting is separate again**: `tool_format.go` (1354 lines) duplicates formatting logic
7. **Dual-write migration layer still present**: `m.output` (bytes.Buffer) and `chatEntries` (ChatEntryList) both maintained

### Goals

- **Performance**: Virtual scrolling — only render visible items
- **Consistency**: Same rendering code for TUI, daemon follow, and IM
- **Clarity**: Each tool call is its own item with clear header/body/status
- **Modularity**: Per-tool-type renderers (like crush's `ToolRenderer` interface)
- **Maintainability**: Remove dual-write, single source of truth for rendering

## Architecture

### Reference Projects

| Aspect | Crush | Claude Code |
|--------|-------|-------------|
| Message model | Interface hierarchy: `list.Item` → `MessageItem` → `ToolMessageItem` | React components per message type |
| Virtual scroll | Custom `List` with offsetIdx/offsetLine | `VirtualMessageList` with `useVirtualScroll` |
| Tool rendering | `ToolRenderer` interface, each tool has its own file | Per-tool React components + `renderGroupedToolUse` |
| Tool status | Enum: pending/running/success/error/canceled | Per-tool-state: queued/waiting/resolved/errored |
| Tool output | Truncate to 10 lines, expandable | Truncate + Ctrl+O to expand |
| Todo/Task | `TodosToolMessageItem` with ratio + body list | `TaskListV2` with priority ordering + fade-out |
| Agent/Subagent | `AgentToolMessageItem` with nested tools in tree | `CoordinatorAgentStatus` + team colors |
| Style system | Central `styles.Styles` struct | Theme system with `ThemedText` |
| Focus/Highlight | `Focusable`/`Highlightable` interfaces | Click-to-select with background highlight |

### Our Design

```
┌─────────────────────────────────────────────────────────────┐
│                     chat/item.go                            │
│  type Item interface {                                       │
│      Render(width int) string                               │
│      ID() string                                            │
│      Height(width int) int                                  │
│  }                                                          │
├─────────────────────────────────────────────────────────────┤
│                     chat/list.go                            │
│  type List struct {                                         │
│      items []Item                                           │
│      offsetIdx, offsetLine int                              │
│      width, height int                                      │
│  }                                                          │
│  // Virtual scroll: only renders visible items              │
├─────────────────────────────────────────────────────────────┤
│  chat/user.go       - User messages (markdown + prefix)     │
│  chat/assistant.go  - Assistant text (markdown streaming)    │
│  chat/tool.go       - Base tool item (header + body + status)│
│  chat/bash.go       - Bash tool renderer                    │
│  chat/file.go       - Read/Write/Edit tool renderer          │
│  chat/search.go     - Grep/Glob/LS tool renderer             │
│  chat/fetch.go      - Fetch/WebSearch tool renderer          │
│  chat/todos.go      - Todo/Task list renderer                │
│  chat/agent.go      - Subagent renderer (nested tools)       │
│  chat/mcp.go        - MCP tool renderer                      │
│  chat/system.go     - System/status messages                 │
│  chat/spacer.go     - Vertical spacing between messages      │
├─────────────────────────────────────────────────────────────┤
│                     chat/styles.go                          │
│  // Centralized style definitions                           │
│  type Styles struct { ... }                                 │
├─────────────────────────────────────────────────────────────┤
│  chat/tool_header.go - Shared tool header rendering          │
│  chat/tool_body.go   - Shared body rendering + truncation    │
└─────────────────────────────────────────────────────────────┘
```

### Item Interface

```go
// chat/item.go
type Item interface {
    // Render produces the ANSI string for this item at the given width.
    Render(width int) string
    
    // ID returns a unique identifier for deduplication and scrolling.
    ID() string
    
    // Height returns the number of visual lines at the given width.
    // Used by the virtual list to calculate scroll positions without rendering.
    Height(width int) int
}

// CachedItem provides common caching for items that are expensive to render.
type CachedItem struct {
    rendered    string
    cachedWidth int
    cachedHeight int
}

func (c *CachedItem) GetCached(width int) (string, int, bool) { ... }
func (c *CachedItem) SetCache(rendered string, width, height int) { ... }
func (c *CachedItem) Invalidate() { ... }
```

### Tool Item Design

Each tool call becomes its own `Item` in the list:

```
✓ Bash   cd /project && go build ./...          ← header: icon + name + params
  [output truncated — 47 lines]                  ← body: truncated output
  ✗ error: undefined: Foo                        ← body: error if any

✓ Edit    internal/tui/model.go                  ← header only for small changes
  - old line                                    ← diff body (optional)

⏸ To-Do  2/5 · writing tests                     ← header: ratio + active task
  ✓ design architecture                         ← body: task list
  → writing tests
  ○ review PR
```

Tool status icons:
- `⏳` pending (animation)
- `✓` success
- `✗` error
- `⊘` canceled

### Todo/Task Rendering

Instead of our current grouped activity blocks:

```
📦 File Operations
  ✓ read config.yaml
  ✓ write main.go
  ⏳ editing test.go
    ... and 3 more steps
```

New design (per-call items, like crush):

```
✓ Read   config.yaml                    ← individual item
✓ Write  main.go                        ← individual item  
⏳ Edit   test.go                       ← individual item with status

⏸ To-Do  3/7 · editing test.go          ← todo update shows full list
  ✓ design architecture
  ✓ implement core
  → editing test.go
  ○ write tests
  ○ review
```

### Subagent Rendering

Instead of our current flat text list, use crush-style nested tree:

```
✓ Agent  Task: implement auth module
  ├ ✓ Read   auth.go
  ├ ✓ Edit   auth.go
  └ ✓ Bash   go test ./auth/...
  Result: All tests passing
```

### Virtual List

```go
// chat/list.go
type List struct {
    items     []Item
    offsetIdx  int    // index of first visible item
    offsetLine int    // line offset within first visible item
    width      int
    height     int
    follow     bool   // auto-scroll to bottom
}

func (l *List) Render() string {
    // Only render items in the visible window
    var lines []string
    needed := l.height
    idx := l.offsetIdx
    offset := l.offsetLine
    
    for needed > 0 && idx < len(l.items) {
        content := l.items[idx].Render(l.width)
        itemLines := strings.Split(content, "\n")
        // Take only visible portion
        visible := itemLines[offset:]
        if len(visible) > needed {
            visible = visible[:needed]
        }
        lines = append(lines, visible...)
        needed -= len(visible)
        idx++
        offset = 0
    }
    return strings.Join(lines, "\n")
}
```

### Unified Rendering Pipeline

All three display contexts share the same item types:

```
TUI (internal/tui)          Daemon Follow         IM (internal/im)
─────────────────          ──────────────        ────────────────
chat.List → Render()       chat.List → Render()  chat.FormatToolResult()
  └ UserItem                  └ UserItem           └ formatUserMessage()
  └ AssistantItem             └ AssistantItem      └ formatAssistantMessage()
  └ BashToolItem              └ BashToolItem       └ formatBashResult()
  └ EditToolItem              └ EditToolItem       └ formatEditResult()
  └ TodoToolItem              └ TodoToolItem       └ formatTodoResult()
  └ AgentToolItem             └ AgentToolItem      └ formatAgentResult()
```

The chat package is a pure rendering library with no TUI/Bubble Tea dependencies. Both `internal/tui` and `internal/daemon` and `internal/im` import it.

## Migration Plan

### Phase 1: Core Infrastructure
1. Create `internal/chat/` package with Item interface, CachedItem, List
2. Implement List with virtual scroll
3. Implement UserItem and AssistantItem (streaming)
4. Wire into TUI as replacement for ChatEntryList
5. Remove m.output / dualWrite

### Phase 2: Tool Items
6. Create base tool item with header/body/status
7. Implement per-tool renderers (Bash, Read, Write, Edit, Grep, Glob, etc.)
8. Wire tool status messages to create/update tool items in the list
9. Remove activity_groups.go

### Phase 3: Structured Items
10. Implement TodoToolItem (ratio + task list)
11. Implement AgentToolItem (nested tools tree)
12. Remove old subagent rendering

### Phase 4: Unification
13. Refactor daemon follow to use chat.List with Render()
14. Refactor IM tool_format.go to use chat tool renderers
15. Delete legacy code (activity_groups.go, tool_format.go duplication, etc.)

## Key Decisions

1. **chat/ package is pure rendering** — no Bubble Tea, no session state, no managers
2. **Items are immutable after creation** — updates create new items (simpler caching)
3. **Virtual scroll is mandatory** — no fallback to full-render
4. **Tool output truncation at 10 lines** — expandable with keypress (like crush)
5. **Each tool call = one item** — no grouping, no activity groups
6. **Same styles everywhere** — TUI, daemon follow, IM all use chat.Styles
