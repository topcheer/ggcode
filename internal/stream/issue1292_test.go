package stream

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

	enc.mu.Lock()
	reaped := enc.exitCh == nil && !enc.running && enc.stdout == nil
	enc.mu.Unlock()
	if !reaped {
		t.Fatal("#1292: Stop returned before the monitor reaped ffmpeg (tail flush not guaranteed)")
	}

	if err := enc.Stop(); err != nil {
		t.Fatalf("double Stop: %v", err)
	}
}

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

	if err := enc.Stop(); err != nil {
		t.Fatalf("Stop after crash: %v", err)
	}
}
