# Developer Tool UX Research — Area 4

**Date:** 2026-07-10  
**Rotation:** 4 of 5 (Developer Tool UX Research)  
**Analyst:** ggcode research agent

---

## Key Findings

1. **Cursor 3.0's Agents Window is the new gold standard for multi-agent UX** — a unified sidebar showing all running agents (local, cloud, remote) at a glance. ggcode has extpane terminal tabs and the TUI follow strip, but lacks a single visual overview of all active sub-agents, swarm teammates, and their progress. The desktop app's TeamBoard partially addresses this but is not visible alongside the chat.

2. **Command palettes are now expected in every developer tool** — Warp (Cmd+P), VS Code (Cmd+Shift+P), Cursor, and Claude Code (/) all provide instant command discovery. ggcode's desktop has Cmd+K command palette, but the TUI relies on slash commands only (no fuzzy search, no Ctrl+P equivalent for quick actions).

3. **Blocks-based output (Warp) fundamentally changes terminal UX** — treating each command+output as a navigable, selectable, shareable unit. ggcode's TUI renders tool results inline but lacks block-level navigation (jump to previous tool result, filter within results, copy entire result block).

4. **Claude Code's Esc+Esc rewind and /btw side-question are unique UX innovations** — Esc+Esc lets users restore code/conversation to a previous point (like an undo for agent actions). /btw asks a quick side question without polluting the main conversation. ggcode has checkpoint support internally but doesn't expose it via a keyboard shortcut.

5. **ggcode's onboarding is config-heavy compared to competitors** — Claude Code auto-detects API keys from environment and starts immediately. Warp requires account creation but guides users through setup. ggcode requires manual config file editing or CLI flags for initial setup, with no interactive first-run wizard.

---

## Detailed Analysis

### A. Keyboard Shortcuts & Navigation

#### Industry Benchmarks

| Feature | Claude Code | Warp | Cursor | ggcode TUI |
|---------|-------------|------|--------|------------|
| Cancel operation | Ctrl+C | - | - | Ctrl+C (double to exit) |
| Clear screen | Ctrl+L | Cmd+K | - | Not implemented |
| Toggle verbose | Ctrl+O | - | - | Not implemented |
| Open in editor | Ctrl+G | - | - | Ctrl+F (file browser) |
| Toggle task list | Ctrl+T | - | - | Ctrl+T (config scope toggle) |
| Permission mode cycle | Shift+Tab | - | - | Not implemented |
| Background command | Ctrl+B | - | - | Not implemented |
| Command palette | / (slash) | Cmd+P | Cmd+Shift+P | / (slash only, no Ctrl+P) |
| Rewind/undo | Esc+Esc | - | Checkpoints | Not exposed |
| Side question | /btw | - | - | Not implemented |
| Model switch | Cmd+P (Meta+P) | - | - | /model (panel) |
| Extended thinking | Cmd+T (Meta+T) | - | - | Ctrl+G (reasoning effort cycle) |
| File reference | @path | - | - | Not implemented |
| Shell prefix | ! | - | - | $ / ! prefix (shell mode) |
| Search history | Ctrl+R | Cmd+R | - | Not implemented (TUI history is up/down arrows only) |
| Image paste | Ctrl+V | - | - | Ctrl+V |
| Multiline | \+Enter | Shift+Enter | Shift+Enter | Shift+Enter |

#### ggcode TUI Keyboard Mapping (from update_keys.go)

| Key | Action |
|-----|--------|
| Ctrl+C | Cancel/exit (double to quit) |
| Ctrl+R | Toggle sidebar |
| Ctrl+G | Cycle reasoning effort |
| Ctrl+F | Toggle file browser |
| Ctrl+T | Toggle config save scope |
| Ctrl+X | Open tmux menu |
| Ctrl+A/E/K/U/W | Emacs-style line editing |
| Ctrl+N/P | History navigation (panel mode) |
| Ctrl+D | Delete character / exit |
| Ctrl+V | Paste image |
| Up/Down arrows | Input history |
| $ / ! | Shell mode prefix |
| # | LAN Chat quick-send |

#### Gaps Identified

1. **No Shift+Tab for mode cycling** — Claude Code users expect Shift+Tab to cycle permission modes. ggcode requires typing `/mode <mode>`. Adding Shift+Tab would significantly improve the mode-switching UX.

2. **No Ctrl+L clear screen** — A basic terminal convention. Ctrl+L should clear the chat view and start fresh (conversation history preserved in session).

3. **No input history search** — Up/down arrow recall works, but there's no Ctrl+R reverse search to filter history by keyword. In long sessions, finding a previous prompt requires many arrow presses.

4. **No rewind shortcut** — ggcode has internal checkpoint support (`internal/checkpoint/`) but doesn't expose it via a keyboard shortcut. Claude Code's Esc+Esc pattern (restore code+conversation to a previous point) would be a high-impact addition.

5. **No @file autocomplete** — Claude Code lets users type `@filename` to reference a file in their prompt, triggering autocomplete. ggcode requires copy-pasting paths or describing files in natural language.

### B. Command Palette & Discovery

#### Industry Patterns

**Warp's Cmd+P** opens a fuzzy-search palette for all terminal commands, workflows, and settings.

**Cursor's Cmd+Shift+P** opens the VS Code command palette with AI-enhanced search.

**Claude Code's /** opens a slash command menu with auto-complete, showing all available commands filtered as you type.

#### ggcode Status

- **TUI**: Slash commands work but require exact name knowledge. No fuzzy search, no autocomplete dropdown. Typing `/` alone shows help text but doesn't filter commands interactively.
- **Desktop**: Has Cmd+K command palette (already implemented in ChatView.tsx).
- **Gap**: TUI slash command autocomplete was recently added to the desktop app but not to TUI. Adding a fuzzy-filtered command dropdown triggered by `/` would significantly improve discoverability.

#### Recommended Pattern: "Type-through" Command Palette

Inspired by lazygit's approach: when the user types `/`, show a dropdown of matching commands that filters as they continue typing. Arrow keys navigate, Enter selects. This doesn't replace the current behavior — it enhances it.

### C. Onboarding & First-Run Experience

#### Industry Benchmarks

| Tool | First-Run Experience |
|------|---------------------|
| Claude Code | Auto-detects `ANTHROPIC_API_KEY` from env, starts immediately with interactive prompt |
| Warp | Account creation, auto-imports shell config, theme picker, interactive tutorial |
| Cursor | Downloads as VS Code fork — familiar interface, AI panel auto-opens |
| Aider | Auto-detects API keys, runs `--help` style onboarding with example commands |
| ggcode | Requires `ggcode.yaml` config or CLI flags; no interactive first-run wizard |

#### ggcode Gaps

1. **No first-run wizard** — New users must understand the vendor/endpoint/model config schema before first use. An interactive wizard that detects installed tools and API keys would dramatically reduce the barrier to entry.

2. **API key detection is passive** — ggcode doesn't proactively scan for common API key env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.) on first run. Claude Code does this automatically.

3. **No example project / demo mode** — Warp ships with sample workflows. Aider shows example commands in its banner. ggcode's empty-session state could include quick-start prompts (already implemented in desktop welcome screen, but not in TUI).

#### Recommended Implementation: Interactive First-Run

```
$ ggcode
 Welcome to ggcode v1.3.139

 Detected: ANTHROPIC_API_KEY in environment
 Configure vendor 'anthropic' with this key? [Y/n]: y
 
 Available models: claude-sonnet-4-20250514, claude-haiku-4-20250514
 Default model [claude-sonnet-4-20250514]: 
 
 Configuration saved to ~/.ggcode/ggcode.yaml
 Starting interactive session...
```

### D. Error Messages & Recovery

#### Industry Patterns

**PatternFly CLI Handbook** (Red Hat, 2025):
- Don't convey meaning through color alone — always include text labels
- Be direct and specific — not "it failed" but "Deployment failed: connection refused"
- Use `--non-interactive` flag for scripting/assistive tech compatibility
- Avoid ASCII tables and dynamic animations for accessibility

**Warp's AI Error Debugging**: When a command fails, highlight the error and press Cmd+I for AI-powered explanation and suggested fixes.

**Claude Code's Friendly Errors**: Provider errors are automatically converted to human-readable messages with actionable next steps.

#### ggcode Status

- Error messages in TUI use color (red) but also include text labels — good
- Provider errors are wrapped with `provider.FriendlyError()` — good
- BUT: tool errors are shown as raw tool output without AI-assisted explanation
- No `--non-interactive` flag for headless/scripting use cases (pipe mode covers this partially)

### E. Blocks & Output Navigation (Warp Pattern)

#### What Warp Does

Each command+output is a "block" — a discrete unit you can:
- Navigate between (Cmd+Up/Cmd+Down)
- Search within (Cmd+F)
- Copy entire output (Cmd+Shift+C)
- Filter output (type to filter when block selected)
- Share as a permalink

#### ggcode Equivalent

ggcode's TUI renders tool results as collapsible bars with expand/collapse. The desktop app has tool result copy buttons, line counts, and truncation indicators. But there are gaps:

1. **No block-level navigation** — Can't jump between tool results with keyboard shortcuts. Must scroll through the entire conversation.
2. **No in-result search** — Can't Ctrl+F within a tool result to find a specific line.
3. **No result-level filtering** — Can't filter a build log to show only "Error" lines.

#### Recommended: Jump-to-Tool-Result Navigation

Add `Ctrl+J` (or `[` / `]`) to jump to the previous/next tool result in the conversation. This would give users Warp-like block navigation without changing the rendering model.

### F. Accessibility

#### Industry Standards (PatternFly, WCAG 2.2)

1. **Color independence**: Never rely on color alone. Always pair with text or icon.
2. **Keyboard accessibility**: All functionality available via keyboard.
3. **Screen reader compatibility**: Structured, labeled output that screen readers can parse.
4. **Contrast**: Minimum 4.5:1 contrast ratio for text.
5. **No auto-animation**: Respect `prefers-reduced-motion`.

#### ggcode Status

- **Color independence**: Mostly good — status indicators use both color and text ("success", "error")
- **Keyboard accessibility**: Good — TUI is entirely keyboard-driven
- **Screen reader compatibility**: Poor — Bubble Tea TUI apps are generally not screen-reader-friendly. The desktop app has `aria-live` on the message container (added in previous research cycle).
- **Contrast**: Depends on terminal theme, not controlled by ggcode
- **Reduced motion**: TUI spinner animations don't respect `prefers-reduced-motion`. Desktop has pulse animations for streaming cursor.

### G. Desktop App (Wails) UX

#### Already Implemented (confirmed from previous research)

- Welcome screen with quick-start prompts
- Command palette (Cmd+K)
- Keyboard shortcut help overlay
- In-conversation search (Cmd+F)
- Slash command autocomplete
- Drag-and-drop image attachment
- Code blocks with copy button + language label
- Date separators
- Scroll-to-bottom button + unread badge
- Context pill with progress bar (green/amber/red)
- Model switcher dropdown
- Input history navigation (up/down arrows)
- GFM task list CSS + table zebra striping
- Copy conversation button
- Accessibility: aria-live on message container
- Tool result copy button + line/char count + truncation

#### Gaps vs Cursor 3.0

1. **No Agent Window equivalent** — Cursor 3.0's unified sidebar showing all running agents. ggcode's TeamBoard exists but is a separate panel, not always visible alongside chat.
2. **No multi-LLM comparison** — Cursor 3.0 can send the same prompt to multiple models and compare outputs side-by-side.
3. **No design mode** — Cursor's visual annotation on browser previews. ggcode has browser automation but no visual annotation overlay.
4. **No `/multitask` equivalent** — Cursor's parallel agent execution from a single prompt. ggcode has `spawn_agent` and `swarm_task_create` but no natural-language "do these 3 things in parallel" shortcut.

---

## Gap Analysis

| Gap | Priority | Effort | Competitor Pattern |
|-----|----------|--------|-------------------|
| **TUI slash command autocomplete** | High | Medium | Claude Code's `/` dropdown |
| **Shift+Tab mode cycling** | High | Low | Claude Code |
| **Ctrl+L clear screen** | High | Low | Universal terminal convention |
| **Ctrl+R input history search** | Medium | Medium | Claude Code, universal terminal |
| **@file path autocomplete** | Medium | Medium | Claude Code |
| **Jump-to-tool-result navigation** | Medium | Medium | Warp blocks |
| **Interactive first-run wizard** | Medium | Medium | Claude Code, Warp |
| **Rewind shortcut (Esc+Esc)** | Medium | High | Claude Code checkpoints |
| **`/btw` side question** | Low | Medium | Claude Code |
| **Non-interactive flag** | Low | Low | PatternFly CLI standard |
| **Reduced motion support** | Low | Low | WCAG 2.2 |

---

## Actionable Recommendations

### 1. **Add Shift+Tab Mode Cycling** (High Impact / Low Effort)

**Pattern:** Claude Code uses Shift+Tab to cycle through permission modes.

**Implementation:** In `update_keys.go`, add:
```go
if msg.String() == "shift+tab" {
    // Cycle: supervised → plan → auto → bypass → autopilot → supervised
    return m.cyclePermissionMode()
}
```

**Effort:** ~20 lines in `update_keys.go` + a `cyclePermissionMode()` method that calls the existing mode-switching logic.

**Next step:** Add the handler to `update_keys.go`, before the panel-handling section.

### 2. **Add Ctrl+L Clear Screen** (High Impact / Low Effort)

**Pattern:** Universal terminal convention. Claude Code uses Ctrl+L to clear the screen while preserving conversation history.

**Implementation:** In `update_keys.go`, add:
```go
if msg.String() == "ctrl+l" {
    // Clear the viewport — conversation history is preserved in the session
    m.chatViewport = ""  // or equivalent clear
    return m, nil
}
```

**Next step:** Add the handler, clearing the viewport content but keeping session messages.

### 3. **TUI Slash Command Autocomplete** (High Impact / Medium Effort)

**Pattern:** Claude Code shows a filterable dropdown when `/` is typed.

**Implementation:** When the input starts with `/` and has at least 1 character after it, show a popup list of matching slash commands. Arrow keys navigate, Enter selects, Esc dismisses. The existing `commandRegistry` already has all commands registered.

**Next step:** Create a `slashAutocomplete` component in the TUI (similar to the existing desktop implementation in ChatView.tsx). Filter `commandRegistry.List()` by prefix.

### 4. **Jump-to-Tool-Result Navigation** (Medium Impact / Medium Effort)

**Pattern:** Warp's block navigation (Cmd+Up/Cmd+Down to jump between command blocks).

**Implementation:** Add `Ctrl+J` (next) and `Ctrl+K` (previous) to jump the viewport to the next/previous tool result bar. This requires tracking tool result positions in the rendered output.

**Next step:** Add a `toolResultOffsets []int` slice to the Model, updated on each render. `Ctrl+J`/`Ctrl+K` scroll to the next/previous offset.

### 5. **Interactive First-Run Wizard** (Medium Impact / Medium Effort)

**Pattern:** Claude Code auto-detects API keys and configures on first launch.

**Implementation:** When `~/.ggcode/ggcode.yaml` doesn't exist and no `--config` flag is provided, run an interactive wizard:
1. Scan for common API key env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.)
2. If found, offer to configure that vendor
3. Ask for preferred model
4. Write config file
5. Start session

**Next step:** Create `cmd/ggcode/onboard.go` with a `runOnboardingWizard()` function. Call from `root.go` when config not found.

### 6. **Ctrl+R Input History Search** (Medium Impact / Medium Effort)

**Pattern:** Universal terminal convention (reverse incremental search through command history).

**Implementation:** When Ctrl+R is pressed, enter a mini-search mode at the bottom of the screen. As the user types, filter the input history and show the most recent match. Enter selects, Ctrl+R again cycles to older matches.

**Next step:** Add an `inputSearchMode` state to the Model. Reuse the existing input history slice (already maintained for up/down arrow recall).

### 7. **@file Path Autocomplete** (Medium Impact / Medium Effort)

**Pattern:** Claude Code's `@` prefix triggers file path autocomplete.

**Implementation:** When the input contains `@` followed by partial text, show a popup of matching file paths (using `glob` or `filepath.Glob`). Arrow keys navigate, Tab/Enter completes.

**Next step:** Add an `@fileAutocomplete` mode to the TUI. Use `os.ReadDir` to list matching files relative to the working directory.

### 8. **Reduced Motion Support** (Low Impact / Low Effort)

**Pattern:** WCAG 2.2 — respect `prefers-reduced-motion`.

**Implementation:** Check the `NO_COLOR` env var or a `ggcode.reduced_motion` config flag. When set, disable spinner animations, streaming cursor pulse, and typing indicator bounce. Replace with static text indicators ("working...", "streaming...").

**Next step:** Add a `reducedMotion bool` field to the Model. Check `os.Getenv("NO_COLOR")` at startup. Guard animation code with `if !m.reducedMotion`.

---

## Sources

- PatternFly CLI Handbook: https://www.patternfly.org/developer-resources/cli-handbook/
- Cursor 3.0 Deep Dive: https://codepick.dev/en/guides/cursor-3-new-features/
- Claude Code Keyboard Shortcuts: https://joeyyu23.github.io/claude-code-handbook/en/book1-getting-started/keyboard-shortcuts
- Warp Terminal Guide 2026: https://aiproductivity.ai/guides/warp-terminal-guide/
- Accessible Terminal (GitHub): https://github.com/ibrasonic/AccessibleTerminal
- NVDA Keyboard Shortcuts: https://dequeuniversity.com/screenreaders/nvda-keyboard-shortcuts
- Cursor 3 Blog: https://cursor.com/blog/cursor-3
- WCAG 2.2 Guidelines: https://www.w3.org/TR/WCAG22/
