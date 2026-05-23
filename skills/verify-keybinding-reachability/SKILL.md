---
name: "verify-keybinding-reachability"
description: "After adding TUI key handlers, trace the full key event dispatch chain to confirm no competing handler intercepts the event before it reaches your new code."
scope: "project"
platforms: ["darwin", "linux", "windows"]
created_by: "knight"
---

# verify-keybinding-reachability

When adding or modifying key bindings in the Bubble Tea TUI, the key event dispatch chain has multiple interceptors (loading guards, autocomplete, preview panels). A new `case` statement does not guarantee the event will reach it.

## Rule

After implementing any key binding change in `model_update.go`, read the **complete key dispatch order** from the top of the `Update` switch to your new handler. Identify every `return m, nil` or `return m, cmd` that fires on the same key **before** your handler. If any earlier branch consumes the event, your handler is dead code. Fix the ordering or add a guard check before committing.

## Steps

1. **Map the dispatch chain**: After writing a new key handler, read `model_update.go` from the first key switch to your new case. List every branch that matches the same key string.
2. **Check for early returns**: For each earlier branch, confirm it either does NOT match the same key or is gated by a condition (e.g., `m.loading`, `m.subAgentFollow.isActive()`) that won't block your intended path.
3. **Verify with grep**: Run `grep -n '"up"\|"down"\|"left"\|"right"\|"ctrl+n"' internal/tui/model_update.go` to see ALL locations where that key is handled, and confirm your handler is reachable.
4. **Test the feature**: After building, describe what the user should see when pressing the key. If you can't run the TUI, at minimum trace the code path by reading each conditional branch in order.
5. **Check for missing UI hints**: If the feature has a keyboard shortcut, verify the hint is shown in the input area when the feature is available (e.g., add "ctrl+n follow" to the hints when subagent/teammate slots exist).

## When to Apply

- Adding new key bindings (arrow keys, Ctrl+combinations, Esc behavior)
- Modifying existing key handling (changing what Esc does, adding guards)
- After any refactoring of the `Update` method's key switch in `model_update.go`
- When implementing features that depend on keyboard navigation between panels

## Examples

**Wrong**: Added arrow key handler at line ~650 in `model_update.go` for follow panel navigation. The code compiled. User reported "上下左右键切换subagent/teammate面板的功能没起作用". The arrow key cases were placed after the autocomplete handler at line ~649 which consumed `"up"` and `"down"` with `return m, cmd` before the follow panel could see them.

**Right**: Before claiming the feature is done, grep for `"up"` in `model_update.go`, see that autocomplete at line 649 returns early, and insert the follow panel guard check (`if m.subAgentFollow.isActive()`) before the autocomplete handler so arrow keys reach the follow panel first.

## Anti-Patterns

- Adding a `case "up":` handler and assuming it works because `go build` passes — key event dispatch order matters, not just compilation.
- Claiming "全部通过" after `go test` without verifying the feature is reachable at runtime — unit tests test isolated logic, not the full dispatch chain.
- Using `sed` or regex for multi-line code edits in Go files — indentation errors are extremely common with tabs; prefer `edit_file` with line-number anchors from `read_file`.
