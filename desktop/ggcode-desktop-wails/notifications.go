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

type NotificationManager struct {
	mu      sync.Mutex
	ctx     interface{}
	focused bool
	enabled bool
	unread  int

	deliveryWarned bool

	lastShown map[string]time.Time

	winQueue chan winToast

	unixQueue chan unixToast

	approvalQueue chan unixToast
}

type winToast struct {
	title string
	body  string
}

type unixToast struct {
	title string
	body  string
}

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

func (nm *NotificationManager) drainWinQueue() {
	for t := range nm.winQueue {
		t := t
		safego.Run("notify-win-toast", func() {
			nm.runWinToast(t.title, t.body)
		})
	}
}

func (nm *NotificationManager) SetContext(ctx interface{}) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.ctx = ctx
}

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

func (nm *NotificationManager) SetFocused(focused bool) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.focused = focused
	if focused {
		nm.unread = 0
		nm.clearBadge()
	}
}

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

const maxLastShownEntries = 256

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

func (nm *NotificationManager) ClearUnread() {
	nm.mu.Lock()
	nm.unread = 0
	nm.mu.Unlock()
	nm.clearBadge()
}

func (nm *NotificationManager) GetUnread() int {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	return nm.unread
}

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

func (nm *NotificationManager) enqueueApproval(title, body string) bool {
	select {
	case nm.approvalQueue <- unixToast{title: title, body: body}:
		return true
	default:
		debug.Log("desktop", "approval queue full; dropping approval banner: %s", title)
		return false
	}
}

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

func (nm *NotificationManager) rollbackNotify(key string) {
	nm.mu.Lock()
	delete(nm.lastShown, key)
	nm.unread--
	if nm.unread < 0 {
		nm.unread = 0
	}
	nm.mu.Unlock()
}

func (nm *NotificationManager) drainUnixQueue() {
	for t := range nm.unixQueue {
		t := t
		safego.Run("notify-unix-toast", func() {
			nm.deliverUnix(t.title, t.body)
		})
	}
}

func escapeAppleScriptText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\x00", "")
	return s
}

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

func (nm *NotificationManager) enqueueWinToast(title, body string) bool {
	select {
	case nm.winQueue <- winToast{title: title, body: body}:
		return true
	default:
		debug.Log("desktop", "Windows toast queue full; dropping notification: %s", title)
		return false
	}
}

func (nm *NotificationManager) runWinToast(title, body string) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := windowsToastScript(title, body)
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script)
	hideConsoleWindow(cmd)
	if err := cmd.Run(); err != nil {
		debug.Log("desktop", "Windows notification failed: %v", err)
	}
}

func psEscapeSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

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
