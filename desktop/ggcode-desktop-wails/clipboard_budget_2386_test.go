package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadClipboardAttachmentsBudget verifies #2386: the read layer enforces
// the #1807 dual budget (max 5 files, 30MB total) BEFORE the payload crosses
// the webview bridge. A Finder multi-select used to read every file fully -
// 200x10MB peaked ~5GB across Go heap + JSON copy + webview parse.
func TestReadClipboardAttachmentsBudget(t *testing.T) {
	dir := t.TempDir()
	// 7 distinct files: over the 5-file cap.
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

// TestReadClipboardAttachmentsByteBudget verifies the total-bytes gate.
// Each file stays under the per-file 10MB cap (that gate is older); four
// 9MB files together cross the 30MB total and the fourth is rejected with
// its data dropped (no partial payload across the bridge).
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
	// 9+9+9 = 27MB passes; the fourth tips 27+9 > 30MB. Binary files carry
	// a business notice in Error ("not pasted as text") - the budget
	// assertion is about the budget marker only.
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
