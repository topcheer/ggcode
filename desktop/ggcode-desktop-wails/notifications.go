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

	// #1809 case 4: delivery failures (TCC denial, notify-send absent)
	// were debug.Log-only - a user who enabled notifications and was
	// denied by the OS had no way to know why nothing arrived. Warn the
	// frontend ONCE per process instead of per failure.
	deliveryWarned bool

	// lastShown keyed by title+body for storm dedup (#398): concurrent
	// sessions completing with identical fixed titles ("Task completed")
	// used to spawn one OS banner per event.
	lastShown map[string]time.Time

	// winQueue serializes Windows toasts (#399): each PowerShell toast used
	// to spawn its own process sleeping 6s; a 10-notification storm held
	// 10 powershell processes (tens of MB each) simultaneously.
	winQueue chan winToast

	// unixQueue serializes macOS/Linux notifications (#1431-A) - the
	// non-Windows twin of winQueue (#399).
	unixQueue chan unixToast
	// approvalQueue is the #2411 fast lane (never shared with bulk).
	approvalQueue chan unixToast
}

// winToast is one queued Windows notification (#399).
type winToast struct {
	title string
	body  string
}

// unixToast is one queued macOS/Linux notification (#1431-A).
type unixToast struct {
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
		unixQueue: make(chan unixToast, 32),

		approvalQueue: make(chan unixToast, 8),
	}

	safego.Go("notify-win-worker", nm.drainWinQueue)

	safego.Go("notify-unix-worker", nm.drainUnixQueue)

	safego.Go("notify-approval-worker", nm.drainApprovalQueue)
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
	nm.enabled = enabled
	if !enabled {
		nm.unread = 0
		nm.clearBadge()
	}
	nm.mu.Unlock()
	if !enabled {

		nm.dropQueuedToasts()
	}
}

// dropQueuedToasts empties both bounded queues non-blockingly (#1866).
func (nm *NotificationManager) dropQueuedToasts() {

	for {
		select {
		case <-nm.approvalQueue:
		default:
			goto approvalDone
		}
	}
approvalDone:
	for {
		select {
		case <-nm.winQueue:
		default:
			goto winDone
		}
	}
winDone:
	for {
		select {
		case <-nm.unixQueue:
		default:
			return
		}
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
//
// #1866 case 1: darwin delivery goes through the bounded unix queue, and a
// full queue must roll back the dedup/unread commits committed above - the
// same contract Windows already has (#600 N4). Without the rollback the
// badge counts banners that will never display and a 5s-window retry hits
// the dedup branch and is never re-queued.
func (nm *NotificationManager) Notify(title, body string) {
	nm.mu.Lock()
	if !nm.enabled {
		nm.mu.Unlock()
		return
	}
	if nm.focused {

		count := nm.unread
		ctx := nm.ctx
		nm.mu.Unlock()
		if ctx != nil {
			if wctx, ok := ctx.(context.Context); ok {
				wailsruntime.EventsEmit(wctx, "notification", map[string]string{
					"title": title,
					"body":  body,
					"count": itoa(count),
				})
			}
		}
		debug.Log("desktop", "notification os-banner suppressed (focused), center event emitted: %s", title)
		return
	}

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
					"count": itoa(count),
				})
			}
		}

		nm.setBadge(count)
		debug.Log("desktop", "notification deduped: %s (unread=%d)", title, count)
		return
	}
	if nm.lastShown == nil {
		nm.lastShown = make(map[string]time.Time)
	}

	if len(nm.lastShown) >= maxLastShownEntries {
		nm.pruneLastShown()
	}
	nm.lastShown[key] = time.Now()
	nm.unread++
	count := nm.unread
	ctx := nm.ctx
	nm.mu.Unlock()

	debug.Log("desktop", "notification: %s - %s (unread=%d)", title, body, count)

	nm.setBadge(count)

	if runtime.GOOS == "windows" {
		if !nm.enqueueWinToast(title, body) {
			nm.rollbackNotify(key)
			debug.Log("desktop", "notification rolled back after toast enqueue failure: %s", title)
		}
	} else if runtime.GOOS == "darwin" {

		if !nm.enqueueUnixToast(title, body) {
			nm.rollbackNotify(key)
			debug.Log("desktop", "notification rolled back after unix enqueue failure: %s", title)
		}
	} else {

		if !nm.showOSNotification(title, body) {
			nm.rollbackNotify(key)
			debug.Log("desktop", "notification rolled back after unix enqueue failure: %s", title)
		}
	}

	if ctx != nil {
		if wctx, ok := ctx.(context.Context); ok {
			wailsruntime.EventsEmit(wctx, "notification", map[string]string{
				"title": title,
				"body":  body,
				"count": itoa(count),
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

	apKey := "approval\x00" + title + "\x00" + body
	if t, ok := nm.lastShown[apKey]; ok && time.Since(t) < 5*time.Second {

		if !nm.focused {
			nm.unread++
		}
		count := nm.unread
		ctxSnap := nm.ctx
		nm.mu.Unlock()
		if ctx := ctxSnap; ctx != nil {
			if wctx, ok := ctx.(context.Context); ok {
				wailsruntime.EventsEmit(wctx, "notification", map[string]string{
					"title": title,
					"body":  body,
					"count": itoa(count),
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

	if !nm.focused {
		nm.unread++
	}
	count := nm.unread
	ctxSnap := nm.ctx
	nm.mu.Unlock()

	debug.Log("desktop", "approval notification: %s - %s", title, body)

	if count > 0 {
		nm.setBadge(count)
	}

	if !nm.enqueueApproval(title, body) {
		nm.mu.Lock()
		delete(nm.lastShown, apKey)
		if !nm.focused && nm.unread > 0 {
			nm.unread--

			count = nm.unread
			if count > 0 {
				nm.setBadge(count)
			}
		}
		nm.mu.Unlock()
		debug.Log("desktop", "approval notification rolled back after approval-lane enqueue failure: %s", title)
	}

	if ctx := ctxSnap; ctx != nil {
		if wctx, ok := ctx.(context.Context); ok {
			wailsruntime.EventsEmit(wctx, "notification", map[string]string{
				"title": title,
				"body":  body,
				"count": itoa(count),
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

// showOSNotification delivers an OS notification synchronously-ish (via
// the platform queue) and reports whether it was accepted for delivery.
// #2095 bug A: the void return was the rollback-contract gap - Linux
// shares darwin's unixQueue (and its queue-full failures) but its callers
// could not roll back without this signal.
func (nm *NotificationManager) showOSNotification(title, body string) bool {
	switch runtime.GOOS {
	case "darwin":
		return nm.notifyMacOS(title, body)
	case "linux":

		return nm.notifyMacOS(title, body)
	case "windows":
		return nm.notifyWindows(title, body)
	}
	return true
}

func (nm *NotificationManager) notifyMacOS(title, body string) bool {

	if !nm.enqueueUnixToast(title, body) {
		debug.Log("desktop", "unix toast queue full; dropping notification: %s", title)
		return false
	}
	return true
}

// enqueueUnixToast offers a toast to the single unix worker queue and
// reports whether it was accepted (#1866). Returns false when the queue
// is full - Notify() must then roll back its lastShown/unread commits,
// mirroring the Windows contract (#600 N4). Platform-independent by
// design so the rollback path stays unit-testable on any host.
// enqueueApproval offers a toast to the dedicated approval lane (#2411).
func (nm *NotificationManager) enqueueApproval(title, body string) bool {
	select {
	case nm.approvalQueue <- unixToast{title: title, body: body}:
		return true
	default:
		debug.Log("desktop", "approval queue full; dropping approval banner: %s", title)
		return false
	}
}

// drainApprovalQueue is the approval-lane worker: one banner at a time,
// never queued behind Task-completed storms (#2411).
// #2411 rework (review): deliver DIRECTLY via the per-platform delivery
// functions, mirroring drainUnixQueue - the previous form routed through
// showOSNotification, which re-enqueued the approval into the BULK OS
// queues (enqueueUnixToast/enqueueWinToast): under a Task-completed storm
// the bulk queue's drop-new silently discarded the banner AND the false
// return was swallowed here while enqueueApproval had already committed,
// so the #600 N4/#2095 rollback in NotifyApprovalNeeded never ran - a
// strictly worse silent loss than the pre-#2411 behavior. #701: per-event
// recover keeps the only approval worker alive.
func (nm *NotificationManager) drainApprovalQueue() {
	for t := range nm.approvalQueue {
		t := t
		if runtime.GOOS == "windows" {
			safego.Run("notify-approval-toast", func() {
				nm.runWinToast(t.title, t.body)
			})
		} else {
			safego.Run("notify-approval-toast", func() {
				nm.deliverUnix(t.title, t.body)
			})
		}
	}
}

func (nm *NotificationManager) enqueueUnixToast(title, body string) bool {
	select {
	case nm.unixQueue <- unixToast{title: title, body: body}:
		return true
	default:
		debug.Log("desktop", "unix toast queue full; dropping notification: %s", title)
		return false
	}
}

// rollbackNotify undoes the dedup-map and unread commits Notify made
// before handing the banner to a bounded queue (#600 N4 / #1866).
func (nm *NotificationManager) rollbackNotify(key string) {
	nm.mu.Lock()
	delete(nm.lastShown, key)
	nm.unread--
	if nm.unread < 0 {
		nm.unread = 0
	}
	nm.mu.Unlock()
}

// drainUnixQueue serializes non-Windows notifications (#1431-A):
// at most ONE osascript/notify-send process is alive at a time.
// #701/#1866: per-event recover - a panicking delivery must skip one
// notification, not kill the only consumer and block every future
// macOS Notify caller on a queue nobody drains.
func (nm *NotificationManager) drainUnixQueue() {
	for t := range nm.unixQueue {
		t := t
		safego.Run("notify-unix-toast", func() {
			nm.deliverUnix(t.title, t.body)
		})
	}
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

// deliverUnix synchronously delivers one non-Windows notification on the
// queue worker: osascript on macOS, notify-send elsewhere (#1431-A).
func (nm *NotificationManager) deliverUnix(title, body string) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if runtime.GOOS == "darwin" {
		script := "display notification \"" + escapeAppleScriptText(body) +
			"\" with title \"" + escapeAppleScriptText(title) +
			"\" sound name \"Glass\""
		cmd := exec.CommandContext(ctx, "osascript", "-e", script)
		if err := cmd.Run(); err != nil {
			debug.Log("desktop", "macOS notification failed: %v", err)
			nm.warnDeliveryOnce("macOS", err)
		}
		return
	}
	cmd := exec.CommandContext(ctx, "notify-send", "--app-name=GGCode", "--icon=dialog-information", title, body)
	if err := cmd.Run(); err != nil {
		debug.Log("desktop", "Linux notification failed: %v", err)
		nm.warnDeliveryOnce("Linux", err)
	}
}

// warnDeliveryOnce surfaces a delivery failure to the frontend exactly
// once per process (#1809 case 4): the OS denied us (permissions, missing
// notifier) and debug.Log alone left the user wondering why nothing
// arrived.
func (nm *NotificationManager) warnDeliveryOnce(platform string, err error) {
	nm.mu.Lock()
	if nm.deliveryWarned {
		nm.mu.Unlock()
		return
	}
	nm.deliveryWarned = true
	wctx, wok := nm.ctx.(context.Context)
	nm.mu.Unlock()
	if !wok {
		return
	}
	wailsruntime.EventsEmit(wctx, "notification:delivery-failed", map[string]string{
		"platform": platform,
		"error":    err.Error(),
		"hint":     "Check the OS notification permission for this app",
	})
}

func (nm *NotificationManager) notifyWindows(title, body string) bool {

	return nm.enqueueWinToast(title, body)
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := windowsToastScript(title, body)
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script)
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

func (nm *NotificationManager) setBadge(count int) {
	if count <= 0 {
		nm.clearBadge()
		return
	}

	if runtime.GOOS == "darwin" {

		script := "tell application \"System Events\" to set dock utilities's badge to \"" + itoa(count) + "\""

		_ = script
	}

}

func (nm *NotificationManager) clearBadge() {
	if runtime.GOOS == "darwin" {

	}
}

func itoa(n int) string { return strconv.Itoa(n) }
