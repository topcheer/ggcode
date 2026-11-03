package extract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssue547_EpubParentDirHrefExtractsText(t *testing.T) {

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
		{"OEBPS/", "../text/chap1.xhtml", "text/chap1.xhtml"},
		{"OEBPS/", "chap1.xhtml", "OEBPS/chap1.xhtml"},
		{"", "chap1.xhtml", "chap1.xhtml"},
		{"OEBPS/", "/abs.xhtml", "abs.xhtml"},
		{"OEBPS/", "./chap1.xhtml", "OEBPS/chap1.xhtml"},
	}
	for _, tc := range cases {
		if got := resolvePath(tc.base, tc.rel); got != tc.want {
			t.Errorf("resolvePath(%q, %q) = %q, want %q", tc.base, tc.rel, got, tc.want)
		}
	}
}

func TestIssue547_RtfHexEscapeValidUTF8(t *testing.T) {

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

	src := `{\rtf1\ansi caf\u233}`
	result, err := Extract("doc.rtf", []byte(src))
	if err != nil {
		t.Fatalf("Extract rtf: %v", err)
	}
	if !strings.Contains(result.Text, "caf\u00e9") {
		t.Errorf("want \"caf\u00e9\" via \\uN, got %q", result.Text)
	}
}

func TestIssue547_ArchiveEntryTruncationKeepsRuneBoundary(t *testing.T) {

	fill := strings.Repeat("汉", 400000)
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

func TestIssue547_TarXzNotRegistered(t *testing.T) {
	if IsDocumentFile("archive.tar.xz") {
		t.Error("IsDocumentFile(.tar.xz) = true, want false — extractor cannot decode xz, registration contradicted IsDocumentFile")
	}
	if defaultRegistry.Get(".tar.xz") != nil {
		t.Error("registry still contains .tar.xz extractor")
	}

	if _, err := Extract("archive.tar.xz", []byte("stub")); err == nil {
		t.Error("Extract(.tar.xz) should return unsupported-format error")
	}
}
