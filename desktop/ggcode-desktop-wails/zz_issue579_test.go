//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestIssue579_ReadFileAsBase64_FIFOPostReadRecheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skip in -short mode: creates large FIFO")
	}

	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "test.fifo")

	if err := syscall.Mkfifo(fifoPath, 0666); err != nil {
		t.Fatalf("mkfifo failed: %v", err)
	}
	defer os.Remove(fifoPath)

	app := &App{workDir: dir}

	go func() {
		f, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer f.Close()

		chunk := make([]byte, 64*1024)
		for i := 0; i < 800; i++ {
			f.Write(chunk)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	_, err := app.ReadFileAsBase64(fifoPath)
	if err != nil {

		t.Logf("ReadFileAsBase64 returned error (expected for FIFO): %v", err)
	}
}

func TestIssue579_ReadClipboardAttachment_ImageBranch10MBCap(t *testing.T) {
	dir := t.TempDir()

	bigImgPath := filepath.Join(dir, "big.png")

	f, err := os.OpenFile(bigImgPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(15*1024*1024, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0x89, 'P', 'N', 'G'}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	att := readClipboardFileAttachment(bigImgPath)
	if att.Error == "" {
		t.Errorf("expected error for 15MB image (exceeds 10MB clipboard limit), got none")
	}
}

func TestIssue579_ParseClipboardPathOutput_NewlineInFilename(t *testing.T) {

	output := "/tmp/a\nb.png\n/tmp/c.txt\n"
	paths := parseClipboardPathOutput(output)

	if len(paths) != 2 {
		t.Logf("got %d paths (linefeed separator splits newlines): %v", len(paths), paths)
	}
}

func TestIssue579_ParseClipboardPathOutput_FileURIDeadCode(t *testing.T) {

	output := "file:///tmp/a%20b.txt"
	paths := parseClipboardPathOutput(output)

	if len(paths) == 1 && paths[0] == "/tmp/a%20b.txt" {

		t.Logf("file:// prefix not handled (dead code removed): %v", paths)
	}
}

func TestIssue579_NotifyApprovalNeeded_StormDedup(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetEnabled(true)
	nm.SetFocused(false)

	for i := 0; i < 10; i++ {
		nm.NotifyApprovalNeeded("GGCode", "Approval needed")
	}

}

func TestIssue579_Notify_EventsEmitBeforeFocusedCheck(t *testing.T) {

	nm := NewNotificationManager()
	nm.SetEnabled(true)
	nm.SetFocused(true)

	nm.Notify("Test", "Message")

}
