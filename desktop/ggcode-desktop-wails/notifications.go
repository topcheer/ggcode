package main

import (
	"context"
	"github.com/topcheer/ggcode/internal/safego"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// NotificationManager handles OS-level desktop notifications.
// It tracks whether the app window is focused: notifications are only
// shown when the window is NOT focused (i.e. the user switched away).
//
// Competitor analysis:
//   - Claude Desktop: shows native notifications when tasks complete
//   - ChatGPT Desktop: notifications + dock bounce on completion
//   - Cursor: in-editor toast only (no OS notification)
//
// ggcode gap: no notification when agent finishes a long task or needs
// approval while the user is in another app.
type NotificationManager struct {
	mu      sync.Mutex
	ctx     interface{} // runtime.Context (stored as interface to avoid import cycles in callers)
	focused bool        // true when the app window has focus
	enabled bool        // master toggle from desktop config
	unread  int         // unread notification count for dock badge

	// lastShown keyed by title+body for storm dedup (#398): concurrent
	// sessions completing with identical fixed titles ("Task completed")
	// used to spawn one OS banner per event.
	lastShown map[string]time.Time

	// winQueue serializes Windows toasts (#399): each PowerShell toast used
	// to spawn its own process sleeping 6s; a 10-notification storm held
	// 10 powershell processes (tens of MB each) simultaneously.
	winQueue chan winToast
}

// winToast is one queued Windows notification (#399).
type winToast struct {
	title string
	body  string
}

// NewNotificationManager creates a notification manager.
// Default: enabled=true, focused=true (assume focused until told otherwise).
func NewNotificationManager() *NotificationManager {
	nm := &NotificationManager{
		focused:   true,
		enabled:   true,
		lastShown: make(map[string]time.Time),
		winQueue:  make(chan winToast, 32),
	}
	// Single worker drains the Windows toast queue serially (#399): at most
	// ONE powershell process is alive at a time regardless of storm size.
	safego.Go("notify-win-worker", nm.drainWinQueue)
	return nm
}

// drainWinQueue serially executes queued Windows toasts (#399).
// #701: per-event recover — a panicking toast must skip one notification,
// not kill the only consumer and block every future notifier on the queue.
func (nm *NotificationManager) drainWinQueue() {
	for t := range nm.winQueue {
		t := t
		safego.Run("notify-win-toast", func() {
			nm.runWinToast(t.title, t.body)
		})
	}
}

// SetContext sets the Wails runtime context.
func (nm *NotificationManager) SetContext(ctx interface{}) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.ctx = ctx
}

// SetEnabled toggles the master notification switch.
func (nm *NotificationManager) SetEnabled(enabled bool) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.enabled = enabled
	if !enabled {
		nm.unread = 0
		nm.clearBadge()
	}
}

// SetFocused updates the window focus state.
// Called from the frontend visibility/focus change events.
func (nm *NotificationManager) SetFocused(focused bool) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.focused = focused
	if focused {
		nm.unread = 0
		nm.clearBadge()
	}
}

// Notify sends a desktop notification if:
// 1. Notifications are enabled
// 2. The window is NOT currently focused
// It also bumps the dock badge count.
func (nm *NotificationManager) Notify(title, body string) {
	nm.mu.Lock()
	if !nm.enabled {
		nm.mu.Unlock()
		return
	}
	if nm.focused {
		// #579: focused sessions still get the in-app notification-center
		// event — previously this early return dropped EventsEmit entirely,
		// so completed-task events never reached the frontend history while
		// the user was looking at the app (the "center still receives every
		// event" comment only held inside the dedup branch). Only the OS
		// banner/badge path is suppressed when focused.
		ctx := nm.ctx
		nm.mu.Unlock()
		if ctx != nil {
			if wctx, ok := ctx.(context.Context); ok {
				wailsruntime.EventsEmit(wctx, "notification", map[string]string{
					"title": title,
					"body":  body,
				})
			}
		}
		debug.Log("desktop", "notification os-banner suppressed (focused), center event emitted: %s", title)
		return
	}
	// Storm dedup (#398): identical title+body within a short window collapses
	// to one OS banner (multi-session completes share fixed titles like
	// "Task completed"). Frontend center still receives every event.
	key := title + "\x00" + body
	if t, ok := nm.lastShown[key]; ok && time.Since(t) < 5*time.Second {
		nm.unread++
		count := nm.unread
		ctx := nm.ctx
		nm.mu.Unlock()
		if ctx != nil {
			if wctx, ok := ctx.(context.Context); ok {
				wailsruntime.EventsEmit(wctx, "notification", map[string]string{
					"title": title,
					"body":  body,
				})
			}
		}
		// #427: the dedup branch bumps unread but skipped setBadge — once the
		// frontend title listener lands (#201), deduped notifications would
		// leave a stale badge count.
		nm.setBadge(count)
		debug.Log("desktop", "notification deduped: %s (unread=%d)", title, count)
		return
	}
	if nm.lastShown == nil {
		nm.lastShown = make(map[string]time.Time)
	}
	// #427: bound lastShown — dynamic-body notifications (job titles with
	// timestamps, error text) used to accumulate one map entry per distinct
	// title+body forever. Keep recent keys only.
	if len(nm.lastShown) >= maxLastShownEntries {
		nm.pruneLastShown()
	}
	nm.lastShown[key] = time.Now()
	nm.unread++
	count := nm.unread
	ctx := nm.ctx
	nm.mu.Unlock()

	debug.Log("desktop", "notification: %s - %s (unread=%d)", title, body, count)

	// Update dock badge
	nm.setBadge(count)

	// Show OS-level notification. #600 N4: on Windows the toast queue can be
	// full; enqueueWinToast reports that and we roll back the dedup-map entry
	// and unread count committed above — otherwise the badge counts a banner
	// that will never display, and a retry within 5s hits the dedup branch
	// and is never re-queued either.
	if runtime.GOOS == "windows" {
		if !nm.enqueueWinToast(title, body) {
			nm.mu.Lock()
			delete(nm.lastShown, key)
			nm.unread--
			if nm.unread < 0 {
				nm.unread = 0
			}
			nm.mu.Unlock()
			debug.Log("desktop", "notification rolled back after toast enqueue failure: %s", title)
		}
	} else {
		nm.showOSNotification(title, body)
	}

	// Also emit to frontend for in-app notification center
	if ctx != nil {
		if wctx, ok := ctx.(context.Context); ok {
			wailsruntime.EventsEmit(wctx, "notification", map[string]string{
				"title": title,
				"body":  body,
			})
		}
	}
}

// NotifyApprovalNeeded sends a high-priority notification when the agent
// needs user interaction (approval or ask_user).
func (nm *NotificationManager) NotifyApprovalNeeded(title, body string) {
	nm.mu.Lock()
	if !nm.enabled {
		nm.mu.Unlock()
		return
	}
	// #579 storm dedup: replayed/concurrent stream events can deliver the
	// same approval request N times; title/body are effectively fixed
	// ("GGCode"/"Approval needed"), so without dedup each replay spawned
	// another osascript process (0.3-1.5s cold start) — #398 covered
	// Notify only. Approval semantics preserved: no focused suppression
	// here; the dedup window collapses the OS banner but still emits the
	// frontend center event.
	apKey := "approval\x00" + title + "\x00" + body
	if t, ok := nm.lastShown[apKey]; ok && time.Since(t) < 5*time.Second {
		// #600 N2: #427 fixed this exact omission in Notify's dedup branch but
		// the sister function was missed — deduped approvals bumped nothing, so
		// replayed requests never moved the badge. Mirror Notify: bump unread
		// when unfocused and refresh the badge.
		if !nm.focused {
			nm.unread++
		}
		count := nm.unread
		ctxSnap := nm.ctx // #450: snapshot under the lock
		nm.mu.Unlock()
		if ctx := ctxSnap; ctx != nil {
			if wctx, ok := ctx.(context.Context); ok {
				wailsruntime.EventsEmit(wctx, "notification", map[string]string{
					"title": title,
					"body":  body,
				})
			}
		}
		if count > 0 {
			nm.setBadge(count)
		}
		debug.Log("desktop", "approval notification deduped: %s (unread=%d)", title, count)
		return
	}
	if nm.lastShown == nil {
		nm.lastShown = make(map[string]time.Time)
	}
	if len(nm.lastShown) >= maxLastShownEntries {
		nm.pruneLastShown()
	}
	nm.lastShown[apKey] = time.Now()
	// Approval notifications show even when focused (they're important),
	// but only bump badge if not focused.
	if !nm.focused {
		nm.unread++
	}
	count := nm.unread
	ctxSnap := nm.ctx // #450: snapshot under the lock — SetContext writes it concurrently
	nm.mu.Unlock()

	debug.Log("desktop", "approval notification: %s - %s", title, body)

	if count > 0 {
		nm.setBadge(count)
	}
	// #600 N4: same queue-full rollback contract as Notify (Windows only).
	if runtime.GOOS == "windows" {
		if !nm.enqueueWinToast(title, body) {
			nm.mu.Lock()
			delete(nm.lastShown, apKey)
			if !nm.focused && nm.unread > 0 {
				nm.unread--
			}
			nm.mu.Unlock()
			debug.Log("desktop", "approval notification rolled back after toast enqueue failure: %s", title)
		}
	} else {
		nm.showOSNotification(title, body)
	}

	// #427: approval notifications must also reach the in-app notification
	// center — Notify() emits "notification" on both paths, but this path
	// never did, so approvals silently bypassed the frontend center.
	if ctx := ctxSnap; ctx != nil {
		if wctx, ok := ctx.(context.Context); ok {
			wailsruntime.EventsEmit(wctx, "notification", map[string]string{
				"title": title,
				"body":  body,
			})
		}
	}
}

// maxLastShownEntries bounds the dedup map (#427).
const maxLastShownEntries = 256

// pruneLastShown drops entries older than the dedup window (callers hold
// nm.mu). If everything is somehow still fresh, evict the oldest entry.
func (nm *NotificationManager) pruneLastShown() {
	cutoff := time.Now().Add(-5 * time.Second)
	for k, t := range nm.lastShown {
		if t.Before(cutoff) {
			delete(nm.lastShown, k)
		}
	}
	if len(nm.lastShown) < maxLastShownEntries {
		return
	}
	var oldestKey string
	var oldestT time.Time
	first := true
	for k, t := range nm.lastShown {
		if first || t.Before(oldestT) {
			oldestKey, oldestT, first = k, t, false
		}
	}
	if oldestKey != "" {
		delete(nm.lastShown, oldestKey)
	}
}

// ClearUnread resets the unread counter and clears the badge.
func (nm *NotificationManager) ClearUnread() {
	nm.mu.Lock()
	nm.unread = 0
	nm.mu.Unlock()
	nm.clearBadge()
}

// GetUnreadCount returns the current unread notification count.
func (nm *NotificationManager) GetUnread() int {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	return nm.unread
}

// --- Platform-specific notification delivery ---

func (nm *NotificationManager) showOSNotification(title, body string) {
	switch runtime.GOOS {
	case "darwin":
		nm.notifyMacOS(title, body)
	case "linux":
		nm.notifyLinux(title, body)
	case "windows":
		nm.notifyWindows(title, body)
	}
}

func (nm *NotificationManager) notifyMacOS(title, body string) {
	// Use osascript to display a native notification.
	// Run asynchronously: osascript cold-start takes 0.3-1.5s and this fires
	// inline on the approval/complete/error agent paths (#290, mirrors the
	// #202 Windows fix). Failures are best-effort and only logged.
	safego.Go("notify-macos", func() {
		script := "display notification \"" + escapeAppleScriptText(body) +
			"\" with title \"" + escapeAppleScriptText(title) +
			"\" sound name \"Glass\""
		cmd := exec.Command("osascript", "-e", script)
		if err := cmd.Run(); err != nil {
			debug.Log("desktop", "macOS notification failed: %v", err)
		}
	})
}

// escapeAppleScriptText makes a string safe to embed in an AppleScript
// double-quoted string literal (#289).
//
// Order matters: backslashes first, then double quotes, to prevent script
// injection via premature quote termination.
//
// Newlines/tabs/CRs are NOT valid inside AppleScript string literals — the
// osascript compiler rejects them ("Expected end of line..."), so a
// multi-line body would fail to compile at runtime. The issue allows either
// AppleScript escaping (`" & linefeed & "` concatenation) or plain spaces;
// we chose single-lining: tab→space, \n→space, \r removed. Notifications
// render one or two lines in the OS banner anyway, so collapsing whitespace
// is visually equivalent and keeps the script simple.
func escapeAppleScriptText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\x00", "")
	return s
}

func (nm *NotificationManager) notifyLinux(title, body string) {
	// Try notify-send (libnotify) — available on most Linux desktops.
	// Run asynchronously (#290): notify-send forks a process on the
	// stream-event dispatch path; keep it off the caller like the #202
	// Windows fix. Failures are best-effort and only logged.
	safego.Go("notify-linux", func() {
		cmd := exec.Command("notify-send", "--app-name=GGCode", "--icon=dialog-information", title, body)
		if err := cmd.Run(); err != nil {
			debug.Log("desktop", "Linux notification failed: %v", err)
		}
	})
}

func (nm *NotificationManager) notifyWindows(title, body string) {
	// Kept for interface parity; real delivery goes through enqueueWinToast so
	// callers can roll back their dedup/unread commits on queue-full (#600 N4).
	_ = nm.enqueueWinToast(title, body)
}

// enqueueWinToast offers a toast to the single worker queue (#399) and
// reports whether it was accepted. Returns false when the queue is full —
// the caller must then roll back its lastShown/unread commits (#600 N4).
// Platform-independent by design (callers gate on GOOS before calling); the
// queue mechanics are identical everywhere, which keeps the rollback path
// unit-testable on non-Windows hosts.
func (nm *NotificationManager) enqueueWinToast(title, body string) bool {
	select {
	case nm.winQueue <- winToast{title: title, body: body}:
		return true
	default:
		debug.Log("desktop", "Windows toast queue full; dropping notification: %s", title)
		return false
	}
}

// runWinToast executes one PowerShell toast synchronously (worker context).
func (nm *NotificationManager) runWinToast(title, body string) {
	script := windowsToastScript(title, body)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	if err := cmd.Run(); err != nil {
		debug.Log("desktop", "Windows notification failed: %v", err)
	}
}

// psEscapeSingleQuotes escapes single quotes for PowerShell single-quoted
// string literals (” doubling). Without this, an apostrophe in the title or
// body closes the literal early, breaking toast syntax (#409).
func psEscapeSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// windowsToastScript builds the PowerShell balloon-toast script.
func windowsToastScript(title, body string) string {
	title = psEscapeSingleQuotes(title)
	body = psEscapeSingleQuotes(body)
	return "[System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms'); " +
		"$balloon = New-Object System.Windows.Forms.NotifyIcon; " +
		"$balloon.Icon = [System.Drawing.SystemIcons]::Information; " +
		"$balloon.BalloonTipTitle = '" + title + "'; " +
		"$balloon.BalloonTipText = '" + body + "'; " +
		"$balloon.Visible = $true; " +
		"$balloon.ShowBalloonTip(5000); " +
		"Start-Sleep -Seconds 6; " +
		"$balloon.Dispose()"
}

// --- Dock badge management ---
// NOTE (#201): the unread state machine (Notify increment, SetFocused/
// SetEnabled/ClearUnread reset, GetUnread read) is kept because the bound
// APIs are used by the frontend, but setBadge/clearBadge are intentionally
// no-ops pending a frontend document.title listener. Emitting a "notification"
// event with an unread count is the integration point when that lands.

func (nm *NotificationManager) setBadge(count int) {
	if count <= 0 {
		nm.clearBadge()
		return
	}
	// Dock badge on macOS via osascript (works for the running app).
	if runtime.GOOS == "darwin" {
		// Use NSApp via osascript to set dock badge.
		script := "tell application \"System Events\" to set dock utilities's badge to \"" + itoa(count) + "\""
		// Actually, setting the badge requires the app's NSApp.
		// On macOS, Wails apps can use NSApp setDockTileBadge.
		// We use a simpler approach: terminal-notifier or osascript display.
		// The badge setting requires platform-specific code that Wails may not expose.
		// For now, the OS notification itself serves as the badge-equivalent.
		_ = script // no-op: badge is handled by notification + frontend title
	}
	// Update document title via frontend event if context available
}

func (nm *NotificationManager) clearBadge() {
	if runtime.GOOS == "darwin" {
		// Same as setBadge — platform limitation noted above.
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
