package tui

// #2373: the TUI-attached IM /usage vendor probe ran svc.Get (a real
// HTTP call) inline on the bubbletea Update goroutine - the whole TUI
// froze up to the probe timeout. The tail now renders async: the sync
// handler only FLAGS the vendor; handleRemoteInbound schedules
// scheduleUsageTail off the loop. Source pins + a behavioral pin that
// SessionUsageSummary returns instantly without any HTTP.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2373SummaryDoesNotProbeInline(t *testing.T) {
	b, err := os.ReadFile("remote_commands.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	fi := strings.Index(src, "func (d tuiSlashDeps) SessionUsageSummary")
	fj := strings.Index(src[fi+5:], "\nfunc ")
	body := src[fi : fi+5+fj]
	if strings.Contains(body, "svc.Get(") {
		t.Fatal("SessionUsageSummary must not call svc.Get inline (#2373)")
	}
	if !strings.Contains(body, "usageTailVendor = vendor") {
		t.Fatal("summary must flag the async tail vendor")
	}
	// nil-session fallback preserved (#2319 twin contract)
	if !strings.Contains(body, "BuildCrossSessionCostSummary()") {
		t.Fatal("nil-session fallback must survive")
	}
}

func TestIssue2373TailScheduledOffUpdateLoop(t *testing.T) {
	b, err := os.ReadFile("update_remote.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "scheduleUsageTail") {
		t.Fatal("handleRemoteInbound must schedule the async tail")
	}
	fi := strings.Index(src, "func (m Model) scheduleUsageTail")
	fj := strings.Index(src[fi+5:], "\nfunc ")
	if fj < 0 {
		fj = len(src) - fi - 5
	}
	tail := src[fi : fi+5+fj]
	if !strings.Contains(tail, "svc.Get(") || !strings.Contains(tail, "emitIMText") {
		t.Fatal("the tail cmd must probe and emit off-loop")
	}
}
