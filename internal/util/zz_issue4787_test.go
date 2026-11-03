package util

import (
	"context"
	"testing"
)

func TestIssue4787_ShellAvailableTrueOnHealthyHost(t *testing.T) {

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

	ResetShellCache()
	ResetShellCache()
	if _, err := DetectShell(); err != nil {
		t.Fatalf("DetectShell after double reset: %v", err)
	}
}

func TestIssue4787_ResetShellCacheConcurrentWithDetect(t *testing.T) {
	const writers = 4
	const resets = 50
	done := make(chan struct{})
	for w := 0; w < writers; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < resets; i++ {
				ResetShellCache()
			}
		}()
	}
	for r := 0; r < writers; r++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < resets; i++ {
				spec, err := DetectShell()
				if err == nil && spec.Path == "" {
					t.Error("DetectShell returned empty spec with nil error")
					return
				}
			}
		}()
	}
	for i := 0; i < writers*2; i++ {
		<-done
	}
	ResetShellCache()
	if _, err := DetectShell(); err != nil {
		t.Fatalf("DetectShell after concurrent reset storm: %v", err)
	}
}

func TestIssue4787_ShellHealthProbeCommandMarker(t *testing.T) {

	cmd, _, err := NewShellCommandContext(context.Background(), ShellHealthProbeCommand)
	if err != nil {
		t.Fatalf("probe command must build: %v", err)
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("probe command must run successfully: %v", err)
	}
}
