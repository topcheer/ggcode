package main

import (
	"io"
	"os"
	"testing"
	"time"
)

func withStdinPipe(t *testing.T, r *os.File, f func()) {
	t.Helper()
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	f()
}

func TestReadStdinEmptyPipeReturnsNil(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	w.Close()

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

func TestReadStdinStalledPipeTimesOut(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

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
			if len(data) != 24 {
				t.Fatalf("data = %d bytes, want 24", len(data))
			}
		case <-time.After(3 * time.Second):
			t.Fatal("readStdin did not complete for a flowing pipe")
		}
	})
	r.Close()
}

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
