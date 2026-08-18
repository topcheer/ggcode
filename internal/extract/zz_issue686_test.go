package extract

// Regression tests for issue #686 (regression of #683):
//  1. SVG decode errors must return the partial text extracted so far,
//     flagged — not hard-drop every extracted segment (the "partial text
//     flagged" path the #683 commit message promised but never shipped).
//  2. An SVG inside an archive whose extraction fails must be visibly
//     marked, not silently disappear from the inventory.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func TestIssue686_SVGDecodeError_PartialTextFlagged(t *testing.T) {
	// Valid text BEFORE an illegal character: the decoder emits text, then
	// errors on the bad byte. The old code returned TextResult{} — the
	// entire extraction vanished.
	svg := `<svg><text>KEEP ME</text><path d="bad & raw"/></svg>`
	// Force a decode error: xml.Decoder.Strict chokes on the bare '&'.
	res, err := Extract("x.svg", []byte(svg))
	if err != nil {
		t.Fatalf("decode errors must surface as flagged text, not error: %v", err)
	}
	if !strings.Contains(res.Text, "KEEP ME") {
		t.Fatalf("partial text before decode error must be kept, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "SVG decode error") {
		t.Fatalf("partial text must be flagged, got %q", res.Text)
	}
}

func TestIssue686_SVGValid_Unchanged(t *testing.T) {
	svg := `<svg><text>hello</text><title>t</title></svg>`
	res, err := Extract("ok.svg", []byte(svg))
	if err != nil {
		t.Fatalf("valid svg: %v", err)
	}
	if !strings.Contains(res.Text, "hello") {
		t.Fatalf("valid svg text missing: %q", res.Text)
	}
	if strings.Contains(res.Text, "decode error") {
		t.Fatalf("valid svg must not carry error flag: %q", res.Text)
	}
}

func TestIssue686_ArchiveSVG_DecodeFailure_VisibleNotSilent(t *testing.T) {
	// Build a tar.gz holding a broken SVG. Old behavior: Extract error on
	// the entry → the entry silently vanished from the listing.
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	brokenSVG := `<svg><text>VISIBILITY</text><path d="bad & bare"/></svg>`
	if err := tw.WriteHeader(&tar.Header{Name: "broken.svg", Mode: 0644, Size: int64(len(brokenSVG))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(brokenSVG)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	gw.Write(tarBuf.Bytes())
	gw.Close()

	res, err := Extract("arch.tar.gz", gzBuf.Bytes())
	if err != nil {
		t.Fatalf("archive extract: %v", err)
	}
	// With the #686 svg fix, the broken SVG yields flagged partial text —
	// it must appear in the archive listing either way (visible), never
	// silently vanish.
	if !strings.Contains(res.Text, "broken.svg") {
		t.Fatalf("archive must list broken.svg entry, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "VISIBILITY") {
		t.Fatalf("partial SVG text must surface in archive listing, got %q", res.Text)
	}
}

func TestIssue686_ArchiveHardErrorEntry_Marked(t *testing.T) {
	// A registry-known extension whose Extract hard-errors (e.g. corrupt
	// zip stored as .zip inside the archive) must be marked, not dropped.
	inner := bytes.Repeat([]byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff, 0xff}, 4) // zip magic + garbage
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{Name: "inner.docx", Mode: 0644, Size: int64(len(inner))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(inner)
	tw.Close()

	res, err := Extract("outer.tar", tarBuf.Bytes())
	if err != nil {
		t.Fatalf("outer archive: %v", err)
	}
	if !strings.Contains(res.Text, "inner.docx") || !strings.Contains(res.Text, "Extraction failed") {
		t.Fatalf("failing inner entry must be visibly marked, got %q", res.Text)
	}
}
