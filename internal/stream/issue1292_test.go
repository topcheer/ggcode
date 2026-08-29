package stream

// Regression tests for GitHub issue #1292: Stop() closed stdin (EOF) but
// immediately closed stdout and Killed ffmpeg before it could flush tail
// frames + FLV trailer (truncated every recording), and a crashed ffmpeg
// was never Wait()ed - zombie process + IsRunning() forever true while the
// manager kept rendering frames into a dead pipe.
//
// Uses a fake "ffmpeg" shell script on PATH: `cat > /dev/null` consumes
// stdin until EOF then exits 0 (graceful finish); `exit 1` simulates a
// crash before reading anything.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func fakeFFmpegDir(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake ffmpeg is unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// Stop must let ffmpeg finish gracefully: after Stop returns, the monitor
// has reaped the process (exitCh cleared under the lock) and IsRunning is
// false - no Kill-before-flush, no zombie.
func TestIssue1292_StopWaitsForGracefulExit(t *testing.T) {
	fakeFFmpegDir(t, "cat > /dev/null\nexit 0\n")
	enc := NewEncoder(4, 4, 26, 1, "")
	if err := enc.Start(); err != nil {
		t.Skipf("fake ffmpeg start failed: %v", err)
	}
	if !enc.IsRunning() {
		t.Fatal("encoder must be running right after Start")
	}
	if err := enc.WriteFrame(make([]byte, 4*4*4)); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	start := time.Now()
	if err := enc.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed > ffmpegGracefulExitTimeout {
		t.Fatalf("Stop took %v - exceeded graceful timeout, fell back to Kill", elapsed)
	}
	// Monitor must have reaped and cleared state before Stop returned.
	enc.mu.Lock()
	reaped := enc.exitCh == nil && !enc.running && enc.stdout == nil
	enc.mu.Unlock()
	if !reaped {
		t.Fatal("#1292: Stop returned before the monitor reaped ffmpeg (tail flush not guaranteed)")
	}
	// Double Stop is a no-op.
	if err := enc.Stop(); err != nil {
		t.Fatalf("double Stop: %v", err)
	}
}

// A crashed ffmpeg must be detected: monitor reaps it and clears running,
// so IsRunning stops reporting a zombie as alive and WriteFrame errors
// instead of writing into a dead pipe forever.
func TestIssue1292_CrashClearsRunning(t *testing.T) {
	fakeFFmpegDir(t, "exit 1\n")
	enc := NewEncoder(4, 4, 26, 1, "")
	if err := enc.Start(); err != nil {
		t.Skipf("fake ffmpeg start failed: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !enc.IsRunning() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if enc.IsRunning() {
		t.Fatal("#1292: crashed ffmpeg still reported running (zombie + fake-alive)")
	}
	if err := enc.WriteFrame(make([]byte, 4*4*4)); err == nil {
		t.Fatal("WriteFrame on a crashed encoder must error, not write to a dead pipe")
	}
	// Stop after a crash must not hang or panic.
	if err := enc.Stop(); err != nil {
		t.Fatalf("Stop after crash: %v", err)
	}
}
