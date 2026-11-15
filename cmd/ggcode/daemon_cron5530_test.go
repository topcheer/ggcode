package main

import (
	"os"
	"strings"
	"testing"
)

// TestIssue5530DaemonReflectionWiresCronOutcome pins the daemon wiring: the
// run-completion reflection must report cron run outcomes through the shared
// adapter, BEFORE the ShouldReflect gate (an idle no-op round is the exact
// shape that gate skips and the no-diff guard must detect). The behavioral
// core of the adapter is covered by internal/agentruntime tests.
func TestIssue5530DaemonReflectionWiresCronOutcome(t *testing.T) {
	src, err := os.ReadFile("daemon_reflection.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	reportIdx := strings.Index(text, "ReportCronRunOutcome(")
	gateIdx := strings.Index(text, "ShouldReflect(")
	if reportIdx < 0 {
		t.Fatal("daemon reflection must report cron run outcomes (#5530)")
	}
	if gateIdx < 0 || reportIdx > gateIdx {
		t.Fatal("cron outcome report must run before the ShouldReflect gate")
	}

	// daemon.go must publish the scheduler into the late-bound slot the
	// reflection getter resolves.
	daemonSrc, err := os.ReadFile("daemon.go")
	if err != nil {
		t.Fatal(err)
	}
	d := string(daemonSrc)
	setupIdx := strings.Index(d, "setupDaemonReflection(ag, workingDir, cronSchedulerRef.Load)")
	storeIdx := strings.Index(d, "cronSchedulerRef.Store(cronScheduler)")
	if setupIdx < 0 {
		t.Fatal("daemon reflection must resolve the scheduler through cronSchedulerRef.Load")
	}
	if storeIdx < 0 {
		t.Fatal("daemon must publish the scheduler via cronSchedulerRef.Store")
	}
}
