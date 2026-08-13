# Computer Use Capability Audit

## Current Capabilities

### 1. Screenshot (`internal/tool/screenshot.go`)
- **Actions**: `capture` (full screen, display, window, region), `list_displays`, `list_windows`
- **Format**: PNG/JPEG, cursor inclusion, delay, resize
- **Gap**: Screenshot is **passive-only** — captures images but can't act on what it sees

### 2. Browser (`internal/tool/browser.go` — 47KB, most capable)
- **Actions**: navigate, click, type, scroll, drag, hover, press, select, evaluate, extract, screenshot, upload, cookies, resize, wait, links, content, status, close, back
- **Tech**: Chrome DevTools Protocol (CDP), Go-native, no external dependencies
- **Gap**: Limited to Chrome/Chromium web pages, cannot interact with native apps

### 3. Mobile Device (`internal/tool/mobile_device.go`)
- **Actions**: devices, boot, install, launch, screenshot, snapshot, tap, type, swipe, press, logs, close, list_apps, uninstall
- **Platforms**: iOS Simulator (XCUITest), Android Emulator/Device (ADB)
- **Gap**: Mobile-only, no desktop OS interaction

### 4. Clipboard (`internal/tool/clipboard.go`)
- Read/write system clipboard

### 5. Terminal Multiplexer Tools
- tmux, Ghostty, iTerm2, Kitty, Warp — pane management, text sending
- **Gap**: Terminal-specific, no general OS GUI interaction

---

## Critical Gaps (Priority-ranked)

### P0: No OS-level mouse/keyboard control

**Problem**: The agent can take screenshots but cannot click, type, or interact with anything outside the browser. This makes "computer use" effectively impossible for:
- Native applications (Calculator, System Settings, Finder, Preview, etc.)
- Desktop IDEs (Xcode, VS Code desktop, Android Studio)
- Any non-browser workflow

**What competitors have**:
- Anthropic Computer Use: `mouse_move(x,y)`, `mouse_click`, `key_press`, `type_text`
- OpenAI Operator: full click+type+scroll on any window
- Claude 3.5 Sonnet computer use API: coordinate-based screen interaction

**Proposed tool**: `desktop_control` or `computer_use`
```json
{
  "actions": ["click", "double_click", "right_click", "move", 
              "type", "key_press", "key_combo", "scroll", 
              "drag", "find_and_click"]
}
```

### P1: No accessibility tree / element detection

**Problem**: When the agent takes a screenshot, it gets raw pixels. There's no way to:
- Find a button by label text ("click the Save button")
- Get UI element coordinates, roles, or labels
- Build a semantic map of the screen

**What competitors have**:
- macOS Accessibility API (AXUIElement) provides full UI tree
- Windows UI Automation (UIA) provides element tree
- Linux AT-SPI provides accessibility tree

**Proposed**: Integrate with OS accessibility APIs to provide:
- `snapshot_ui` — returns element tree (role, label, frame, enabled, focused)
- `find_element(text)` — returns coordinates of matching UI element
- This enables "find and click" without coordinate guessing

### P2: No window management

**Problem**: Cannot:
- List/activate/focus windows by title or app name
- Minimize, maximize, restore windows
- Move or resize windows
- Close windows (outside browser)

**Proposed tool**: `window_control`
```json
{
  "actions": ["list", "activate", "close", "minimize", "maximize", 
              "move", "resize", "bring_to_front"]
}
```

### P3: No application launch/control

**Problem**: Cannot launch applications by name or bundle ID.

**Proposed**: Extend `desktop_control` with:
- `launch(app)` — open application by name or bundle ID
- `quit(app)` — gracefully quit application
- `is_running(app)` — check if app is running

### P4: No multi-step GUI workflow orchestration

**Problem**: Each tool call is atomic. No way to chain "screenshot → find element → click → verify" in a single deterministic step. The LLM has to reason about coordinates between calls, which is error-prone.

**Proposed**: Composite action `find_and_click(text)` that internally:
1. Takes screenshot
2. Uses accessibility tree or OCR to find element
3. Computes center coordinates
4. Performs click
5. Returns result

### P5: Screenshot resolution mismatch

**Problem**: Screenshots on Retina displays are 2x resolution. If the agent sees a 3840x2160 screenshot but coordinates are in 1920x1080 logical pixels, clicks land in wrong places.

**Fix**: Normalize coordinates between logical (points) and physical (pixels), expose both in screenshot metadata.

---

## Implementation Priority

| Priority | Feature | Effort | Impact |
|----------|---------|--------|--------|
| P0 | OS mouse/keyboard control | Medium (platform-specific) | Enables ALL native app interaction |
| P1 | Accessibility tree snapshot | Large (per-platform API) | Enables precise element targeting |
| P2 | Window management | Small | Enables multi-app workflows |
| P3 | App launch/control | Small | Quick wins |
| P4 | Composite find-and-click | Medium (depends on P1) | Reduces LLM reasoning errors |
| P5 | Retina coordinate fix | Small | Correctness on HiDPI displays |

---

## Recommended First Step

Start with **P0 (mouse/keyboard) + P2 (window management) + P3 (app launch)** as a single `desktop_control` tool. This provides immediate "computer use" capability without the complexity of accessibility API integration.

For macOS, use `cg.EventSourceCreate` + `cg.EventPost` (Core Graphics) for mouse/keyboard events. For Linux, use `xdotool` or `uinput`. For Windows, use `SendInput`.

Accessibility tree (P1) can be added incrementally as an enhancement to enable element-based targeting instead of coordinate-based.
