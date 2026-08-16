package extract

// #566 feature tests: zip memory amplification (E), RTF destination
// skipping + \bin skipping (A), surrogate pairs + \uc fallback (B),
// EPUB/iWork empty-success distinction (G), ODT page count (F).

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// --- E: zip streaming read budget ---

func TestIssue566_ZipEntryReadBudget(t *testing.T) {
	// Build a zip with one large plain-text entry (500KB). The preview
	// budget must cap the buffered bytes far below the entry size.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	chunk := []byte(strings.Repeat("a", 4096))
	for i := 0; i < 128; i++ { // 512KB total
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
	// A nested .zip entry must still be read whole (capped at
	// maxArchiveEntrySize) so the nested extractor can parse it.
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

	// End-to-end: the nested content must still be extractable.
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
	// Many structured entries: the cumulative cap must stop total buffering
	// at maxZipTotalRead, keeping memory bounded.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	big := bytes.Repeat([]byte("z"), 512*1024) // 512KB per entry, 200 entries = 100MB
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
	// Cap plus per-entry slack for the entries already in flight.
	if total > maxZipTotalRead+maxArchiveEntrySize {
		t.Errorf("cumulative buffered %d bytes exceeds budget %d", total, maxZipTotalRead)
	}
	_ = r
}

// --- A: RTF destination skipping and \bin ---

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
	// \bin4 followed by 4 raw binary bytes; those bytes must not appear.
	res := rtfExtract(t, `{\rtf1 before \bin4`+"\x00\x01\xFF\x10"+` after}`)
	if strings.Contains(res.Text, "\x00") || strings.Contains(res.Text, "\xFF\x10") {
		t.Errorf("binary leaked into text: %q", res.Text)
	}
	if !strings.Contains(res.Text, "before") || !strings.Contains(res.Text, "after") {
		t.Errorf("text around \\bin lost: %q", res.Text)
	}
}

// --- B: surrogate pairs and \uc fallback ---

func TestIssue566_RTF_SurrogatePair(t *testing.T) {
	// U+1F600 emoji = \uD83D\uDE00 in UTF-16 surrogate halves.
	res := rtfExtract(t, `{\rtf1 grin \u55357\u56832!}`)
	if !strings.Contains(res.Text, "\U0001F600") {
		t.Errorf("surrogate pair not combined: %q", res.Text)
	}
	if strings.Count(res.Text, "\uFFFD") != 0 {
		t.Errorf("replacement chars in output: %q", res.Text)
	}
}

func TestIssue566_RTF_UCFallbackSubstituteDropped(t *testing.T) {
	// \uc1\u233 ? — the '?' is the ANSI substitute and must not double
	// the character.
	res := rtfExtract(t, `{\rtf1 \uc1\u233 ?est}`)
	if !strings.Contains(res.Text, "\u00E9est") {
		t.Errorf("expected 'éest', got %q", res.Text)
	}
	if strings.Contains(res.Text, "?est") {
		t.Errorf("substitute '?' leaked after \\uN: %q", res.Text)
	}
}

func TestIssue566_RTF_UCFallbackHexDropped(t *testing.T) {
	// \uc2\u233 \'e9\'e9 — two hex substitutes must both be dropped.
	res := rtfExtract(t, "{\\rtf1 \\uc2\\u233 \\'e9\\'e9end}")
	if !strings.Contains(res.Text, "\u00E9end") {
		t.Errorf("expected 'éend', got %q", res.Text)
	}
	if strings.Count(res.Text, "\u00E9") != 1 {
		t.Errorf("fallback bytes duplicated: %q", res.Text)
	}
}

func TestIssue566_RTF_UnpairedSurrogateFlushed(t *testing.T) {
	// A high surrogate never followed by its low half degrades to a single
	// U+FFFD, not two and not silence.
	res := rtfExtract(t, `{\rtf1 \u55357 end}`)
	if strings.Count(res.Text, "\uFFFD") != 1 {
		t.Errorf("unpaired surrogate should yield exactly one U+FFFD, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "end") {
		t.Errorf("text after unpaired surrogate lost: %q", res.Text)
	}
}

// --- G: EPUB / iWork empty-success distinction ---

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
		href = `text\chapter1.xhtml` // Windows-tool style: never matches a ZIP entry
	}
	mustAdd("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0"><manifest><item id="c1" href="`+href+`" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`)
	mustAdd(entryName, `<html><body><p>chapter text here</p></body></html>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIssue566_EPUBBackslashSpineIsError(t *testing.T) {
	// A broken spine (backslash hrefs) previously returned Text:"" err:nil,
	// indistinguishable from an empty book. Now it must be an explicit error.
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
	// iWork 2013+ archives contain only binary .iwa parts. Previously this
	// returned Text:"" err:nil, hiding the unsupported format.
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

// --- F: ODT page count ---

func TestIssue566_ODTPageCountFromBreaks(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("content.xml")
	if err != nil {
		t.Fatal(err)
	}
	// 3 paragraphs separated by explicit page breaks: 3 pages.
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
