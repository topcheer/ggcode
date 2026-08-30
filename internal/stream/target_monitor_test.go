package stream

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #1304: the target-side ffmpeg had no exit monitor - when it exited on its
// own (network drop / stream key rejected) nothing reaped it, and Connect's
// re-entry overwrote t.cmd, accumulating zombies. The monitor goroutine must
// reap the process and flip a Live target to TargetError with a reason.
//
// Uses a fake ffmpeg (shell script) injected via PATH: it answers -version
// for CheckFFmpeg and exits immediately when asked to stream, simulating a
// target that dies right after Connect.
func TestTargetMonitorReapsExitedFFmpeg(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  -version) echo 'ffmpeg version 6.1.2 fake'; exit 0;;\n" +
		"  *) exit 9;;\n" + // simulate immediate target exit
		"esac\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	target := NewTarget("test", "rtmp://example.invalid/app/key")
	if _, err := target.Connect(); err != nil {
		t.Fatalf("Connect with fake ffmpeg: %v", err)
	}

	// The fake exits immediately; the monitor must reap it and flip the
	// Live target to TargetError with a recorded reason.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if target.State() == TargetError {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := target.State(); got != TargetError {
		t.Fatalf("expected TargetError after ffmpeg exit, got %v", got)
	}
	if target.Status().LastError == "" {
		t.Error("expected exit reason recorded in LastError")
	}

	// Stop after the process already exited must be safe (monitor owns Wait).
	done := make(chan struct{})
	go func() {
		target.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked after monitor already reaped the process")
	}
}
