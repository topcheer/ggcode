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

// #1339: after Stop() -> quick Connect(), a late-returning monitor for the
// OLD ffmpeg must not flip the healthy NEW session to TargetError. The
// monitor callback must verify t.cmd == monCmd, not just state == Live.
func TestTargetMonitorStaleCmdDoesNotKillNewSession(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  -version) echo 'ffmpeg version 6.1.2 fake'; exit 0;;\n" +
		"  *) exec sleep 30;;\n" + // stay alive until killed
		"esac\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	target := NewTarget("test", "rtmp://example.invalid/app/key")
	if _, err := target.Connect(); err != nil {
		t.Fatalf("first Connect: %v", err)
	}
	target.Stop() // kills the first ffmpeg; its monitor's Wait returns soon

	// Reconnect before the old monitor necessarily ran its callback.
	if _, err := target.Connect(); err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	if target.State() != TargetLive {
		t.Fatalf("new session should be Live, got %v", target.State())
	}

	// Give the old monitor's late Wait() callback ample time to fire. The
	// new session must remain Live (old cmd identity check) and writable.
	time.Sleep(500 * time.Millisecond)
	if got := target.State(); got != TargetLive {
		t.Fatalf("stale monitor corrupted new session: state=%v lastError=%q", got, target.Status().LastError)
	}
	if _, err := target.Write([]byte("flvdata")); err != nil {
		t.Fatalf("new session should still be writable: %v", err)
	}
	target.Stop()
}
