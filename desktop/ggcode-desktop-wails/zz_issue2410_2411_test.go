package main

// #2410: every "notification" emit carries the unread count (the #201
// document.title listener contract); the frontend ChatView subscribes.
// #2411: approvals ride a dedicated queue+worker, never the 32-slot
// bulk lanes whose drop-new under backlog would discard exactly the
// highest-priority signal.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestIssue2410EveryNotificationEmitCarriesCount(t *testing.T) {
	b, err := os.ReadFile("notifications.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	emitRe := regexp.MustCompile(`EventsEmit\(wctx, "notification", map\[string\]string\{`)
	n := len(emitRe.FindAllString(s, -1))
	if n < 5 {
		t.Fatalf("expected >=5 notification emit sites, found %d", n)
	}
	if got := strings.Count(s, `"count": itoa(count)`); got != n {
		t.Fatalf("emit sites=%d but count-armed=%d (every emit must carry count)", n, got)
	}
	fe, err := os.ReadFile("frontend/src/components/ChatView.tsx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fe), `EventsOn('notification'`) {
		t.Fatal("frontend must subscribe to 'notification' (#2410: zero subscribers was the bug)")
	}
}

func TestIssue2411ApprovalDedicatedLane(t *testing.T) {
	b, err := os.ReadFile("notifications.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"approvalQueue: make(chan unixToast, 8)",
		`safego.Go("notify-approval-worker", nm.drainApprovalQueue)`,
		"case nm.approvalQueue <-",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing approval fast-lane element: %q", want)
		}
	}
	// NotifyApprovalNeeded must no longer enqueue into the bulk lanes.
	i := strings.Index(s, "func (nm *NotificationManager) NotifyApprovalNeeded")
	j := strings.Index(s[i+5:], "\nfunc ")
	if j < 0 {
		j = len(s) - i - 5
	} else {
		j += i + 5
	}
	body := s[i:j]
	for _, banned := range []string{"enqueueWinToast(title, body)", "showOSNotification(title, body)"} {
		if strings.Contains(body, banned) {
			t.Fatalf("approval path still uses bulk lane: %q", banned)
		}
	}
	if !strings.Contains(body, "enqueueApproval(title, body)") {
		t.Fatal("approval path must enqueue into the dedicated lane")
	}
}

// TestIssue2411ApprovalWorkerNeverReentersBulkLane pins the #2411 rework:
// the approval worker must DELIVER directly (deliverUnix / runWinToast),
// never route through showOSNotification -> enqueueUnixToast/enqueueWinToast
// which re-queued the banner behind Task-completed storms and swallowed the
// queue-full false return after enqueueApproval had already committed (a
// silent loss with no rollback). The body-slicing pins on
// NotifyApprovalNeeded alone missed this - the worker is pinned in its own
// function body.
func TestIssue2411ApprovalWorkerNeverReentersBulkLane(t *testing.T) {
	b, err := os.ReadFile("notifications.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "func (nm *NotificationManager) drainApprovalQueue")
	if i < 0 {
		t.Fatal("drainApprovalQueue not found")
	}
	j := strings.Index(s[i:], "\nfunc ")
	if j < 0 {
		j = len(s) - i
	} else {
		j += i
	}
	body := s[i:j]
	for _, banned := range []string{
		"showOSNotification",
		"enqueueUnixToast",
		"enqueueWinToast",
	} {
		if strings.Contains(body, banned) {
			t.Fatalf("approval worker re-enters the bulk lane: %q", banned)
		}
	}
	for _, want := range []string{
		"deliverUnix",
		"runWinToast",
		`safego.Run("notify-approval-toast"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("approval worker missing direct-delivery element: %q", want)
		}
	}
}
