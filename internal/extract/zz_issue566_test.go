package extract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestIssue566_ZipEntryReadBudget(t *testing.T) {

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	chunk := []byte(strings.Repeat("a", 4096))
	for i := 0; i < 128; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	files, err := listZip(buf.Bytes())
	if err != nil {
		t.Fatalf("listZip: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	if len(files[0].data) > maxZipEntryRead+1 {
		t.Errorf("plain entry buffered %d bytes, budget is %d", len(files[0].data), maxZipEntryRead)
	}
}

func TestIssue566_ZipStructuredEntryFullRead(t *testing.T) {

	var inner bytes.Buffer
	izw := zip.NewWriter(&inner)
	iw, _ := izw.Create("note.txt")
	iw.Write([]byte("nested hello"))
	izw.Close()

	var outer bytes.Buffer
	ozw := zip.NewWriter(&outer)
	ow, _ := ozw.Create("inner.zip")
	ow.Write(inner.Bytes())
	if f, _ := ozw.Create("plain.txt"); true {
		f.Write([]byte("plain text entry"))
	}
	ozw.Close()

	files, err := listZip(outer.Bytes())
	if err != nil {
		t.Fatalf("listZip: %v", err)
	}
	var nested *archiveFile
	for i := range files {
		if files[i].name == "inner.zip" {
			nested = &files[i]
		}
	}
	if nested == nil {
		t.Fatal("nested zip entry missing from listing")
	}
	if len(nested.data) != inner.Len() {
		t.Errorf("nested entry buffered %d bytes, want full %d", len(nested.data), inner.Len())
	}

	result, err := Extract("test.zip", outer.Bytes())
	if err != nil {
		t.Fatalf("Extract zip: %v", err)
	}
	if !strings.Contains(result.Text, "nested hello") {
		t.Errorf("nested zip content lost; text=%q", result.Text)
	}
}

func TestIssue566_ZipCumulativeBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a larger zip")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	big := bytes.Repeat([]byte("z"), 512*1024)
	for i := 0; i < 200; i++ {
		name := "doc" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".epub"
		w, _ := zw.Create(name)
		w.Write(big)
	}
	zw.Close()

	var total int64
	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files, err := listZip(buf.Bytes())
	if err != nil {
		t.Fatalf("listZip: %v", err)
	}
	for _, f := range files {
		total += int64(len(f.data))
	}

	if total > maxZipTotalRead+maxArchiveEntrySize {
		t.Errorf("cumulative buffered %d bytes exceeds budget %d", total, maxZipTotalRead)
	}
	_ = r
}

func rtfExtract(t *testing.T, src string) TextResult {
	t.Helper()
	res, err := Extract("test.rtf", []byte(src))
	if err != nil {
		t.Fatalf("rtf extract: %v", err)
	}
	return res
}

func TestIssue566_RTF_SkipsFontTable(t *testing.T) {
	res := rtfExtract(t, `{\rtf1\ansi{\fonttbl{\f0 Arial;}{\f1 Symbol;}}\f0\fs24 Real body text}`)
	if strings.Contains(res.Text, "Arial") || strings.Contains(res.Text, "Symbol") {
		t.Errorf("font table leaked into body: %q", res.Text)
	}
	if !strings.Contains(res.Text, "Real body text") {
		t.Errorf("body text lost: %q", res.Text)
	}
}

func TestIssue566_RTF_SkipsColorTableAndInfo(t *testing.T) {
	res := rtfExtract(t, `{\rtf1{\colortbl;\red255\green0\blue0;}{\info{\author John Doe;\company Acme}}Hello document}`)
	for _, leak := range []string{"red255", "John Doe", "Acme"} {
		if strings.Contains(res.Text, leak) {
			t.Errorf("destination content %q leaked: %q", leak, res.Text)
		}
	}
	if !strings.Contains(res.Text, "Hello document") {
		t.Errorf("body text lost: %q", res.Text)
	}
}

func TestIssue566_RTF_BinarySkipped(t *testing.T) {

	res := rtfExtract(t, `{\rtf1 before \bin4`+"\x00\x01\xFF\x10"+` after}`)
	if strings.Contains(res.Text, "\x00") || strings.Contains(res.Text, "\xFF\x10") {
		t.Errorf("binary leaked into text: %q", res.Text)
	}
	if !strings.Contains(res.Text, "before") || !strings.Contains(res.Text, "after") {
		t.Errorf("text around \\bin lost: %q", res.Text)
	}
}

func TestIssue566_RTF_SurrogatePair(t *testing.T) {

	res := rtfExtract(t, `{\rtf1 grin \u55357\u56832!}`)
	if !strings.Contains(res.Text, "\U0001F600") {
		t.Errorf("surrogate pair not combined: %q", res.Text)
	}
	if strings.Count(res.Text, "\uFFFD") != 0 {
		t.Errorf("replacement chars in output: %q", res.Text)
	}
}

func TestIssue566_RTF_UCFallbackSubstituteDropped(t *testing.T) {

	res := rtfExtract(t, `{\rtf1 \uc1\u233 ?est}`)
	if !strings.Contains(res.Text, "\u00E9est") {
		t.Errorf("expected 'éest', got %q", res.Text)
	}
	if strings.Contains(res.Text, "?est") {
		t.Errorf("substitute '?' leaked after \\uN: %q", res.Text)
	}
}

func TestIssue566_RTF_UCFallbackHexDropped(t *testing.T) {

	res := rtfExtract(t, "{\\rtf1 \\uc2\\u233 \\'e9\\'e9end}")
	if !strings.Contains(res.Text, "\u00E9end") {
		t.Errorf("expected 'éend', got %q", res.Text)
	}
	if strings.Count(res.Text, "\u00E9") != 1 {
		t.Errorf("fallback bytes duplicated: %q", res.Text)
	}
}

func TestIssue566_RTF_UnpairedSurrogateFlushed(t *testing.T) {

	res := rtfExtract(t, `{\rtf1 \u55357 end}`)
	if strings.Count(res.Text, "\uFFFD") != 1 {
		t.Errorf("unpaired surrogate should yield exactly one U+FFFD, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "end") {
		t.Errorf("text after unpaired surrogate lost: %q", res.Text)
	}
}

func buildEPUB(t *testing.T, spineHrefMode string, entryName string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mustAdd := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd("mimetype", "application/epub+zip")
	mustAdd("META-INF/container.xml", `<?xml version="1.0"?><container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`)
	href := "chapter1.xhtml"
	if spineHrefMode == "backslash" {
		href = `text\chapter1.xhtml`
	}
	mustAdd("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0"><manifest><item id="c1" href="`+href+`" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`)
	mustAdd(entryName, `<html><body><p>chapter text here</p></body></html>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIssue566_EPUBBackslashSpineIsError(t *testing.T) {

	data := buildEPUB(t, "backslash", "OEBPS/text/chapter1.xhtml")
	_, err := Extract("book.epub", data)
	if err == nil {
		t.Fatal("backslash-spine EPUB must return an error, not empty success")
	}
	if !strings.Contains(err.Error(), "spine") {
		t.Errorf("error should mention the spine: %v", err)
	}
}

func TestIssue566_EPUBGoodSpineStillWorks(t *testing.T) {
	data := buildEPUB(t, "normal", "OEBPS/chapter1.xhtml")
	res, err := Extract("book.epub", data)
	if err != nil {
		t.Fatalf("good EPUB failed: %v", err)
	}
	if !strings.Contains(res.Text, "chapter text here") {
		t.Errorf("text missing: %q", res.Text)
	}
	if res.Pages != 1 {
		t.Errorf("chapters = %d, want 1", res.Pages)
	}
}

func TestIssue566_EPUBEmptySpineIsError(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("mimetype")
	w.Write([]byte("application/epub+zip"))
	w, _ = zw.Create("META-INF/container.xml")
	w.Write([]byte(`<?xml version="1.0"?><container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`))
	w, _ = zw.Create("content.opf")
	w.Write([]byte(`<?xml version="1.0"?><package><manifest></manifest><spine></spine></package>`))
	zw.Close()

	_, err := Extract("empty.epub", buf.Bytes())
	if err == nil {
		t.Fatal("empty-spine EPUB must return an error")
	}
}

func TestIssue566_IWorkIWAOnlyIsError(t *testing.T) {

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("Index.zip.iwa")
	w.Write([]byte{0x00, 0x01, 0x02, 0xFF})
	w, _ = zw.Create("Metadata/Properties.plist")
	w.Write([]byte("bplist00"))
	zw.Close()

	_, err := Extract("doc.pages", buf.Bytes())
	if err == nil {
		t.Fatal(".iwa-only iWork archive must return an error, not empty success")
	}
	if !strings.Contains(err.Error(), "iwa") && !strings.Contains(err.Error(), "xml") {
		t.Errorf("error should explain the unsupported .iwa content: %v", err)
	}
}

func TestIssue566_ODTPageCountFromBreaks(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("content.xml")
	if err != nil {
		t.Fatal(err)
	}

	content := `<?xml version="1.0"?><office:document-content><office:body><office:text>` +
		`<text:p>page one</text:p><text:p><style:style/></text:p>` +
		`<text:p><text:page-break/>page two</text:p>` +
		`<text:p><text:page-break/>page three</text:p>` +
		`</office:text></office:body></office:document-content>`
	w.Write([]byte(content))
	zw.Close()

	res, err := Extract("doc.odt", buf.Bytes())
	if err != nil {
		t.Fatalf("ODT extract: %v", err)
	}
	if res.Pages != 3 {
		t.Errorf("pages = %d, want 3 (text=%q)", res.Pages, res.Text)
	}
	if !strings.Contains(res.Text, "page three") {
		t.Errorf("text missing page three: %q", res.Text)
	}
}
