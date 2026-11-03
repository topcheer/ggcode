package cmdpane

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriter_TruncatesLargeStaleLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "stale.log")

	large := strings.Repeat("x", maxCmdPaneLogSize+1024)
	if err := os.WriteFile(logPath, []byte(large), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= maxCmdPaneLogSize {
		t.Fatalf("expected initial size > %d, got %d", maxCmdPaneLogSize, info.Size())
	}

	if info.Size() > maxCmdPaneLogSize {
		if err := os.Truncate(logPath, 0); err != nil {
			t.Fatal(err)
		}
	}

	info, err = os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected truncated size 0, got %d", info.Size())
	}
}

func TestWriter_PreservesSmallLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "small.log")
	small := strings.Repeat("x", 100)
	if err := os.WriteFile(logPath, []byte(small), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}

	if info.Size() > maxCmdPaneLogSize {
		t.Fatalf("expected size <= %d, got %d (should not truncate)", maxCmdPaneLogSize, info.Size())
	}

	info, err = os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 100 {
		t.Fatalf("expected size 100, got %d", info.Size())
	}
}
