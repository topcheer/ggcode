package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16LEWithBOM(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := []byte{0xFF, 0xFE}
	for _, r := range u {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func TestIssue671_UTF16TxtNotLabelledTextPlain(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "utf16.txt")
	if err := os.WriteFile(p, utf16LEWithBOM("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	att := readClipboardFileAttachment(p)
	if att.Error == "" {
		t.Fatalf("UTF-16 content must still be rejected as binary (intentional heuristic), got %+v", att)
	}
	if att.Kind != "binary" {
		t.Fatalf("expected Kind=binary, got %q", att.Kind)
	}
	if strings.HasPrefix(att.MimeType, "text/") {
		t.Fatalf("#671 regression: binary-rejected attachment still labelled %q while Kind=%q Error=%q — fields contradict",
			att.MimeType, att.Kind, att.Error)
	}
	if att.MimeType != "application/octet-stream" {
		t.Fatalf("expected sniffed-and-downgraded application/octet-stream, got %q", att.MimeType)
	}
}

func TestIssue671_RenamedImageExtNotTrusted(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fake.png")

	if err := os.WriteFile(p, []byte{0x00, 0x01, 0x02, 0x03, 0xFE, 0xFF}, 0644); err != nil {
		t.Fatal(err)
	}

	att := readClipboardFileAttachment(p)
	if att.Error == "" {
		t.Fatalf("expected binary rejection, got %+v", att)
	}
	if strings.HasPrefix(att.MimeType, "image/") {
		t.Fatalf("#671 regression: extension-derived %q kept for non-image content", att.MimeType)
	}
	if att.MimeType != "application/octet-stream" {
		t.Fatalf("expected application/octet-stream, got %q", att.MimeType)
	}
}

func TestIssue671_UTF8TextKeepsRicherExtMime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(p, []byte("a,b\n1,2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	att := readClipboardFileAttachment(p)
	if att.Error != "" {
		t.Fatalf("UTF-8 CSV must paste as text, got error %q", att.Error)
	}
	if att.Kind != "text" {
		t.Fatalf("expected Kind=text, got %q", att.Kind)
	}
	if !strings.HasPrefix(att.MimeType, "text/csv") {
		t.Fatalf("expected richer extension mime text/csv to be kept, got %q", att.MimeType)
	}
}

func TestIssue671_UTF8TextUnknownExtSniffed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notes.weirdext")
	if err := os.WriteFile(p, []byte("plain utf-8 text"), 0644); err != nil {
		t.Fatal(err)
	}

	att := readClipboardFileAttachment(p)
	if att.Kind != "text" || att.Error != "" {
		t.Fatalf("expected accepted text, got Kind=%q Error=%q", att.Kind, att.Error)
	}
	if !strings.HasPrefix(att.MimeType, "text/plain") {
		t.Fatalf("expected sniffed text/plain, got %q", att.MimeType)
	}
}

func TestIssue671_ImageBranchUnchanged(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "dot.txt")
	if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	att := readClipboardFileAttachment(p)
	if att.Error != "" {
		t.Fatalf("real image must attach, got error %q", att.Error)
	}
	if att.Kind != "image" {
		t.Fatalf("expected Kind=image (content wins over extension), got %q", att.Kind)
	}
	if !strings.HasPrefix(att.MimeType, "image/png") {
		t.Fatalf("expected content-sniffed image/png, got %q", att.MimeType)
	}
	if att.Data == "" {
		t.Fatal("expected base64 data payload")
	}
}

func TestIssue671_NoSelfContradictionOnAnyPath(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"utf8.txt":     []byte("hello"),
		"utf16.txt":    utf16LEWithBOM("hello"),
		"binary.bin":   {0x00, 0x01, 0xFF, 0xFE, 0x7F},
		"misext.csv":   {0xDE, 0xAD, 0xBE, 0xEF, 0x00},
		"misext2.json": utf16LEWithBOM(`{"a":1}`),
	}
	for name, data := range cases {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatal(err)
		}
		att := readClipboardFileAttachment(p)
		if att.Kind == "binary" && strings.HasPrefix(att.MimeType, "text/") {
			t.Errorf("%s: binary rejection with text/* mime %q — fields contradict", name, att.MimeType)
		}
		if att.Kind == "text" && !strings.HasPrefix(att.MimeType, "text/") {
			t.Errorf("%s: accepted text with non-text mime %q — fields contradict", name, att.MimeType)
		}
	}
}
