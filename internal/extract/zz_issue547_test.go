package extract

// Issue #547 characteristic tests: EPUB ../ href resolution (A), RTF \'XX
// code-page decoding (D), archive CJK rune-boundary truncation (B), and the
// .tar.xz registration contract fix (C).

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

// --- Bug A: EPUB ../ href resolution ---

func TestIssue547_EpubParentDirHrefExtractsText(t *testing.T) {
	// Layout common in Sigil/older InDesign exports: OPF lives in OEBPS/,
	// chapter files in a sibling text/ dir reached via "../text/chap1.xhtml".
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	mustAdd := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	mustAdd("mimetype", "application/epub+zip")
	mustAdd("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	mustAdd("OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
  <manifest>
    <item id="c1" href="../text/chap1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/></spine>
</package>`)
	mustAdd("text/chap1.xhtml", `<html><body><p>Chapter One, the sharp bruiser.</p></body></html>`)

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := Extract("book.epub", buf.Bytes())
	if err != nil {
		t.Fatalf("Extract epub: %v", err)
	}
	if result.Pages == 0 {
		t.Errorf("Pages = 0, want >= 1 — ../ href failed to resolve, chapter silently skipped")
	}
	if !strings.Contains(result.Text, "Chapter One") {
		t.Errorf("text missing chapter content, got %q", result.Text)
	}
}

func TestIssue547_ResolvePathNormalization(t *testing.T) {
	cases := []struct{ base, rel, want string }{
		{"OEBPS/", "../text/chap1.xhtml", "text/chap1.xhtml"}, // the #547 case
		{"OEBPS/", "chap1.xhtml", "OEBPS/chap1.xhtml"},        // no regression
		{"", "chap1.xhtml", "chap1.xhtml"},                    // no regression
		{"OEBPS/", "/abs.xhtml", "abs.xhtml"},                 // no regression
		{"OEBPS/", "./chap1.xhtml", "OEBPS/chap1.xhtml"},      // ./ also normalized
	}
	for _, tc := range cases {
		if got := resolvePath(tc.base, tc.rel); got != tc.want {
			t.Errorf("resolvePath(%q, %q) = %q, want %q", tc.base, tc.rel, got, tc.want)
		}
	}
}

// --- Bug D: RTF \'XX code-page decoding ---

func TestIssue547_RtfHexEscapeValidUTF8(t *testing.T) {
	// \ansicpg1252 caf\'e9 — the exact probe input from the issue.
	src := `{\rtf1\ansi\ansicpg1252 caf\'e9}`
	result, err := Extract("doc.rtf", []byte(src))
	if err != nil {
		t.Fatalf("Extract rtf: %v", err)
	}
	if !utf8.ValidString(result.Text) {
		t.Errorf("output is invalid UTF-8: %q", result.Text)
	}
	if !strings.Contains(result.Text, "caf\u00e9") {
		t.Errorf("want \"caf\u00e9\" (e9 -> U+00E9), got %q", result.Text)
	}
}

func TestIssue547_RtfHexEscapeDefaultCodePage(t *testing.T) {
	// No \ansicpg control word at all: the spec default is Windows-1252.
	src := `{\rtf1\ansi r\'e9sum\'e9}`
	result, err := Extract("doc.rtf", []byte(src))
	if err != nil {
		t.Fatalf("Extract rtf: %v", err)
	}
	if !strings.Contains(result.Text, "r\u00e9sum\u00e9") {
		t.Errorf("want \"r\u00e9sum\u00e9\" with 1252 default, got %q", result.Text)
	}
	if !utf8.ValidString(result.Text) {
		t.Errorf("output is invalid UTF-8: %q", result.Text)
	}
}

func TestIssue547_RtfHexEscapeRespectsAnsicpg1251(t *testing.T) {
	// cp1251 0xE9 = U+0439 (й). Proves the decoder actually reads \ansicpg
	// rather than always assuming 1252 (which would give U+00E9 é).
	src := `{\rtf1\ansi\ansicpg1251 \'e9}`
	result, err := Extract("doc.rtf", []byte(src))
	if err != nil {
		t.Fatalf("Extract rtf: %v", err)
	}
	if !strings.Contains(result.Text, "\u0439") {
		t.Errorf("cp1251 e9 should decode to U+0439 (й), got %q", result.Text)
	}
}

func TestIssue547_RtfUnicodeEscapeUnchanged(t *testing.T) {
	// The \uN path was already correct; guard against regressions.
	src := `{\rtf1\ansi caf\u233}`
	result, err := Extract("doc.rtf", []byte(src))
	if err != nil {
		t.Fatalf("Extract rtf: %v", err)
	}
	if !strings.Contains(result.Text, "caf\u00e9") {
		t.Errorf("want \"caf\u00e9\" via \\uN, got %q", result.Text)
	}
}

// --- Bug B: archive entry truncation at rune boundary ---

func TestIssue547_ArchiveEntryTruncationKeepsRuneBoundary(t *testing.T) {
	// To make a registered-format entry's extracted text exceed
	// maxArchiveEntrySize, exploit zip nesting: an .odt whose content.xml
	// decompresses to ~1.2MB of CJK fits well under the archive entry data
	// cap, but its extracted text crosses it. The truncation point
	// (1048576 % 3 == 1) then falls mid-rune — the exact bug B probe.
	fill := strings.Repeat("汉", 400000) // 1.2MB of valid UTF-8
	contentXML := "<t>" + fill + "</t>"

	var odt bytes.Buffer
	izw := zip.NewWriter(&odt)
	w, err := izw.Create("content.xml")
	if err != nil {
		t.Fatalf("create content.xml: %v", err)
	}
	if _, err := w.Write([]byte(contentXML)); err != nil {
		t.Fatalf("write content.xml: %v", err)
	}
	if err := izw.Close(); err != nil {
		t.Fatalf("close odt zip: %v", err)
	}
	if odt.Len() > maxArchiveEntrySize {
		t.Fatalf("test setup: odt payload (%d bytes) must stay under the entry data cap", odt.Len())
	}

	var outer bytes.Buffer
	ozw := zip.NewWriter(&outer)
	ow, err := ozw.Create("doc.odt")
	if err != nil {
		t.Fatalf("create doc.odt: %v", err)
	}
	if _, err := ow.Write(odt.Bytes()); err != nil {
		t.Fatalf("write doc.odt: %v", err)
	}
	if err := ozw.Close(); err != nil {
		t.Fatalf("close outer zip: %v", err)
	}

	result, err := Extract("bundle.zip", outer.Bytes())
	if err != nil {
		t.Fatalf("Extract zip: %v", err)
	}
	if !strings.Contains(result.Text, "(truncated)") {
		t.Fatalf("expected truncation to fire (text len %d), tail %q", len(result.Text), tailFor(result.Text, 60))
	}
	if !utf8.ValidString(result.Text) {
		// Locate the offending byte for the failure message.
		bad := -1
		for i, r := range result.Text {
			if r == utf8.RuneError {
				bad = i
				break
			}
		}
		t.Errorf("extracted text contains invalid UTF-8 near byte %d (mid-rune truncation)", bad)
	}
}

func tailFor(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// --- Bug C: .tar.xz no longer registered ---

func TestIssue547_TarXzNotRegistered(t *testing.T) {
	if IsDocumentFile("archive.tar.xz") {
		t.Error("IsDocumentFile(.tar.xz) = true, want false — extractor cannot decode xz, registration contradicted IsDocumentFile")
	}
	if defaultRegistry.Get(".tar.xz") != nil {
		t.Error("registry still contains .tar.xz extractor")
	}
	// Unregistered must surface an honest error instead of guaranteed failure.
	if _, err := Extract("archive.tar.xz", []byte("stub")); err == nil {
		t.Error("Extract(.tar.xz) should return unsupported-format error")
	}
}
