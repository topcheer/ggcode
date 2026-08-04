# Desktop File Drag-and-Drop

## Overview

ggcode Desktop supports native OS file drag-and-drop. Users can drag files from
Finder (macOS), Explorer (Windows), or their file manager (Linux) directly into
the desktop window. Dropped file paths are inserted into the chat input as
backtick-quoted references that the agent can read.

## Competitor Analysis

| App                | Native File DnD | Path Insertion | Image DnD |
|--------------------|:--------------:|:--------------:|:---------:|
| Claude Desktop     | Yes            | Yes            | Yes       |
| Cursor             | Yes            | Yes            | Yes       |
| ChatGPT Desktop    | Yes            | Yes            | Yes       |
| GitHub Copilot     | N/A (IDE)      | N/A            | N/A       |
| Windsurf           | Yes            | Yes            | Yes       |
| **ggcode Desktop** | **Yes**        | **Yes**        | **Yes**   |

## Implementation

### Backend (Go / Wails v2)

**`main.go`** -- Enables Wails' native drag-and-drop in the app options:

```go
DragAndDrop: &options.DragAndDrop{
    EnableFileDrop:     true,
    DisableWebViewDrop: true,  // prevent webview from opening files
    CSSDropProperty:    "--wails-drop-target",
    CSSDropValue:       "drop",
},
```

**`app.go`** -- Registers `runtime.OnFileDrop` handler in `startup()`:

```go
runtime.OnFileDrop(ctx, func(x, y int, paths []string) {
    a.enqueueUIEvent("file:drop", map[string]interface{}{
        "x": x, "y": y, "paths": paths,
    })
})
```

### Frontend (React / TypeScript)

**`ChatView.tsx`** -- Listens for the `file:drop` event and inserts file paths
into the input box as backtick-quoted references:

```typescript
EventsOn('file:drop', (data: { paths: string[] }) => {
    const pathRefs = data.paths.map(p => `\`${p}\``).join('\n')
    setInput(prev => `${prev}${pathRefs}\n`)
})
```

## How It Works

1. User drags file(s) from OS file manager into the window
2. Wails intercepts the drop at the native level (not webview)
3. `runtime.OnFileDrop` callback receives absolute file paths
4. Go emits `file:drop` event to frontend via `enqueueUIEvent`
5. ChatView inserts paths as backtick references into the input
6. User can edit/add context, then send the message
7. Agent receives the file paths and can use `read_file` to read them

## Image Files

Images dragged from the clipboard (via the existing HTML5 `handleDrop`) are
already handled separately -- they are converted to base64 attachments. The
native Wails DnD handles all file types (text, code, images, binary) by
inserting their path reference.
