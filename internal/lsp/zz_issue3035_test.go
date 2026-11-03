package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestIssue3035_ExecutableExistsRequiresExecBit(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "gopls")
	if err := os.WriteFile(plain, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "gopls-ok")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		if executableExists(plain) {
			t.Fatal("#3035: a 0644 file without any execute bit must not count as an executable server")
		}
	}
	if !executableExists(executable) {
		t.Fatal("0755 file must be accepted")
	}

	if executableExists(dir) {
		t.Fatal("directory must not count")
	}
	if executableExists(filepath.Join(dir, "nope")) {
		t.Fatal("missing path must not count")
	}
}

func TestIssue3035_ExternalProbeTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script probe is unix-shaped; windows CI covers the build")
	}
	dir := t.TempDir()
	slow := filepath.Join(dir, "slowprobe")
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(slow, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err := runExternalProbe(slow)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a 30s sleeper must fail via the probe timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline-exceeded error, got: %v", err)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("probe must return near the 8s bound, took %s", elapsed)
	}

	echo := filepath.Join(dir, "fastprobe")
	if err := os.WriteFile(echo, []byte("#!/bin/sh\necho hello\n"), 0755); err != nil {
		t.Fatal(err)
	}
	out, err := runExternalProbe(echo)
	if err != nil || strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("fast probe must succeed, out=%q err=%v", out, err)
	}
}
