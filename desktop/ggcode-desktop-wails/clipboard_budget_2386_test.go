package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadClipboardAttachmentsBudget(t *testing.T) {
	dir := t.TempDir()

	var paths []string
	for i := 0; i < 7; i++ {
		p := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	atts := readClipboardAttachmentsFromPaths(paths)

	if len(atts) != 7 {
		t.Fatalf("got %d attachments, want 7 (5 kept + 2 budget rows)", len(atts))
	}
	kept := 0
	for _, att := range atts {
		if att.Error == "" {
			kept++
		} else if !strings.Contains(att.Error, "budget exceeded") {
			t.Fatalf("unexpected error %q", att.Error)
		}
	}
	if kept != 5 {
		t.Fatalf("kept %d files, want exactly the 5-file cap", kept)
	}
}

func TestReadClipboardAttachmentsByteBudget(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, "b"+string(rune('a'+i))+".bin")
		if err := os.WriteFile(p, make([]byte, 9<<20), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	atts := readClipboardAttachmentsFromPaths(paths)
	if len(atts) != 4 {
		t.Fatalf("got %d attachments, want 4", len(atts))
	}

	for i, att := range atts[:3] {
		if strings.Contains(att.Error, "budget exceeded") {
			t.Fatalf("file %d (cumulative %dMB) wrongly budget-rejected: %q", i, (i+1)*9, att.Error)
		}
	}
	if !strings.Contains(atts[3].Error, "budget exceeded") {
		t.Fatalf("fourth file must be budget-rejected, got %q", atts[3].Error)
	}
	if atts[3].Data != "" {
		t.Fatalf("budget-rejected file must not carry data across the bridge")
	}
}
