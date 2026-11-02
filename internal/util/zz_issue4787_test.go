package util

import (
	"context"
	"testing"
	"time"
)

// #4787 (S-16): the shell channel can fail wholesale (fork/exec ENOENT on
// /bin/sh, 1-8min, self-healing). The probe API must (a) actually spawn the
// shell — a stat-only check would pass while the channel is unspawnable —
// and (b) allow the once-cached detection to be reset.

func TestIssue4787_ShellAvailableTrueOnHealthyHost(t *testing.T) {
	// On a healthy host the probe must report the channel as spawnable.
	if !ShellAvailable() {
		t.Fatal("ShellAvailable must be true on a healthy host")
	}
}

func TestIssue4787_ResetShellCacheReprobes(t *testing.T) {
	ResetShellCache()
	spec, err := DetectShell()
	if err != nil {
		t.Fatalf("DetectShell after reset: %v", err)
	}
	if spec.Path == "" {
		t.Fatal("DetectShell after reset returned an empty path")
	}
	// Repeated resets must be safe (no once-latch corruption).
	ResetShellCache()
	ResetShellCache()
	if _, err := DetectShell(); err != nil {
		t.Fatalf("DetectShell after double reset: %v", err)
	}
}

func TestIssue4787_ShellHealthProbeCommandMarker(t *testing.T) {
	// The marker must stay a self-contained no-op shell command: it is both
	// the degraded-gate instruction and the stagnation exemption marker.
	cmd, _, err := NewShellCommandContext(context.Background(), ShellHealthProbeCommand)
	if err != nil {
		t.Fatalf("probe command must build: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmdCtx, _, _ := NewShellCommandContext(ctx, ShellHealthProbeCommand)
	_ = cmdCtx
	if err := cmd.Run(); err != nil {
		t.Fatalf("probe command must run successfully: %v", err)
	}
}
