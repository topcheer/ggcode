package stream

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTargetMonitorReapsExitedFFmpeg(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  -version) echo 'ffmpeg version 6.1.2 fake'; exit 0;;\n" +
		"  *) exit 9;;\n" +
		"esac\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	target := NewTarget("test", "rtmp://example.invalid/app/key")
	if _, err := target.Connect(); err != nil {
		t.Fatalf("Connect with fake ffmpeg: %v", err)
	}

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

func TestTargetMonitorStaleCmdDoesNotKillNewSession(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  -version) echo 'ffmpeg version 6.1.2 fake'; exit 0;;\n" +
		"  *) exec sleep 30;;\n" +
		"esac\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	target := NewTarget("test", "rtmp://example.invalid/app/key")
	if _, err := target.Connect(); err != nil {
		t.Fatalf("first Connect: %v", err)
	}
	target.Stop()

	if _, err := target.Connect(); err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	if target.State() != TargetLive {
		t.Fatalf("new session should be Live, got %v", target.State())
	}

	time.Sleep(500 * time.Millisecond)
	if got := target.State(); got != TargetLive {
		t.Fatalf("stale monitor corrupted new session: state=%v lastError=%q", got, target.Status().LastError)
	}
	if _, err := target.Write([]byte("flvdata")); err != nil {
		t.Fatalf("new session should still be writable: %v", err)
	}
	target.Stop()
}
