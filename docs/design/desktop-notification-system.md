# Desktop Notification System

## Overview

GGCode Desktop sends OS-level notifications when the agent finishes a task, encounters an error, or needs user interaction (approval/ask_user) -- but only when the app window is **not focused** (the user has switched to another application).

This matches the behavior of Claude Desktop and ChatGPT Desktop, ensuring users never miss important agent events while working in other apps.

## How It Works

### Window Focus Tracking
The frontend (`Layout.tsx`) listens to `visibilitychange`, `window.blur`, and `window.focus` events and reports focus state to the Go backend via `App.SetWindowFocused(focused: boolean)`.

### Notification Triggers
The Go backend (`app.go`) monitors stream events and triggers notifications for:
- **Task completion** (`complete` event): "Task completed"
- **Errors** (`error` event): "An error occurred"
- **Approval requests** (`approval:request`): "Approval needed" -- fires even when focused (high priority)
- **Agent questions** (`ask_user:request`): "Question from agent" -- fires even when focused

### Platform-Specific Delivery
- **macOS**: `osascript` displays native notifications with "Glass" sound
- **Linux**: `notify-send` (libnotify) for desktop notifications
- **Windows**: PowerShell balloon tip via `System.Windows.Forms.NotifyIcon`

### Unread Badge Tracking
When notifications fire while unfocused, an unread counter increments. Returning to the app (focus) automatically resets the counter.

## Configuration

Notifications are enabled by default and can be toggled via:
- **Go API**: `App.SetNotificationsEnabled(enabled: boolean)` -- persists to `~/.ggcode/desktop-config.json`
- **Config file**: `notifications_enabled: true` in `desktop-config.json`

## Competitor Comparison

| Feature | GGCode | Claude Desktop | ChatGPT Desktop | Cursor |
|---------|--------|----------------|-----------------|--------|
| OS notification on completion | Yes | Yes | Yes | No |
| OS notification on error | Yes | No | No | No |
| OS notification on approval | Yes | Yes | No | N/A |
| Suppress when focused | Yes | Yes | Yes | N/A |
| Configurable toggle | Yes | Yes | Yes | N/A |
| Cross-platform | Yes | Yes | Yes | N/A |

## Files

- `desktop/ggcode-desktop-wails/notifications.go` -- NotificationManager (OS notifications, focus tracking, badge)
- `desktop/ggcode-desktop-wails/notifications_test.go` -- 10 unit tests
- `desktop/ggcode-desktop-wails/app.go` -- Notification wiring in stream events + frontend API methods
- `desktop/ggcode-desktop-wails/frontend/src/components/Layout.tsx` -- Window focus/visibility tracking
- `desktop/wailskit/desktop_config.go` -- Notification preference persistence
