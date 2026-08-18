package main

import (
	"io"
	"os"
	"testing"
	"time"
)

// withStdinPipe swaps os.Stdin for r and restores it after f returns.
func withStdinPipe(t *testing.T, r *os.File, f func()) {
	t.Helper()
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	f()
}

// TestReadStdinEmptyPipeReturnsNil covers #537 Bug E (prefix half): a pipe
// that is closed with zero bytes must yield nil, not []byte{} — otherwise
// buildPipePrompt's nil check fails and the prompt gains a "\n\n" prefix.
func TestReadStdinEmptyPipeReturnsNil(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	w.Close() // empty, immediately closed

	withStdinPipe(t, r, func() {
		data, err := readStdin()
		if err != nil {
			t.Fatalf("readStdin: %v", err)
		}
		if data != nil {
			t.Fatalf("readStdin on empty pipe = %#v, want nil", data)
		}
	})
	r.Close()
}

// TestBuildPipePromptEmptyStdinNoPrefix covers #537 Bug E at the prompt
// level: empty piped stdin must NOT prepend "\n\n" to the user prompt.
func TestBuildPipePromptEmptyStdinNoPrefix(t *testing.T) {
	prompt, blocks, err := buildPipePrompt("hi", nil)
	if err != nil {
		t.Fatalf("buildPipePrompt: %v", err)
	}
	if blocks != nil {
		t.Fatalf("expected nil blocks, got %v", blocks)
	}
	if prompt != "hi" {
		t.Fatalf("prompt = %q, want %q", prompt, "hi")
	}
}

// TestReadStdinStalledPipeTimesOut covers #537 Bug E (blocking half): a pipe
// whose writer never closes and never writes must not block forever —
// readStdin returns (nil, nil) after the idle timeout instead of hanging.
func TestReadStdinStalledPipeTimesOut(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close() // writer stays open but silent — the stall case

	origTimeout := stdinIdleTimeout
	stdinIdleTimeout = 100 * time.Millisecond
	defer func() { stdinIdleTimeout = origTimeout }()

	withStdinPipe(t, r, func() {
		done := make(chan struct{})
		var data []byte
		var readErr error
		go func() {
			data, readErr = readStdin()
			close(done)
		}()

		select {
		case <-done:
			if readErr != nil {
				t.Fatalf("readStdin returned error: %v", readErr)
			}
			if data != nil {
				t.Fatalf("stalled pipe data = %#v, want nil", data)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("readStdin blocked forever on a stalled pipe (no idle timeout)")
		}
	})
}

// TestReadStdinSlowButFlowingPipeCompletes guards the timeout semantics: the
// deadline is per-read (idle), so a stream that keeps delivering data within
// the window finishes normally even if total time exceeds the window.
func TestReadStdinSlowButFlowingPipeCompletes(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	origTimeout := stdinIdleTimeout
	stdinIdleTimeout = 500 * time.Millisecond
	defer func() { stdinIdleTimeout = origTimeout }()

	go func() {
		for i := 0; i < 4; i++ {
			w.WriteString("chunk\n")
			time.Sleep(100 * time.Millisecond)
		}
		w.Close()
	}()

	withStdinPipe(t, r, func() {
		done := make(chan struct{})
		var data []byte
		var readErr error
		go func() {
			data, readErr = readStdin()
			close(done)
		}()

		select {
		case <-done:
			if readErr != nil {
				t.Fatalf("readStdin: %v", readErr)
			}
			if len(data) != 24 { // 4 * len("chunk\n")
				t.Fatalf("data = %d bytes, want 24", len(data))
			}
		case <-time.After(3 * time.Second):
			t.Fatal("readStdin did not complete for a flowing pipe")
		}
	})
	r.Close()
}

// TestReadStdinRegularFile reads a regular file redirected to stdin (the
// `ggcode -p hi < file.txt` case): full contents, no timeout path.
func TestReadStdinRegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdin-*.txt")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.WriteString("file contents here"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	defer f.Close()

	withStdinPipe(t, f, func() {
		data, err := readStdin()
		if err != nil {
			t.Fatalf("readStdin: %v", err)
		}
		if string(data) != "file contents here" {
			t.Fatalf("data = %q", string(data))
		}
	})
}

// TestReadStdinCharDeviceSkipped: when stdin is a terminal (char device),
// readStdin returns (nil, nil) without reading. /dev/null is not a char
// device on all platforms, so this is validated indirectly via the closed-fd
// behavior in the empty-pipe test plus this structural check.
func TestReadStdinCharDeviceSkipped(t *testing.T) {
	if info, err := os.Stdin.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) != 0 {
		data, err := readStdin()
		if err != nil {
			t.Fatalf("readStdin on char device: %v", err)
		}
		if data != nil {
			t.Fatalf("char device data = %#v, want nil", data)
		}
	}
}
