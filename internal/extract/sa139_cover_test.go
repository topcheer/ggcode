package extract

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// sa139_cover_test.go - research round sa-139 coverage net for internal/extract.
// Test-only: exercises real code paths with real bytes (no mocks of package
// internals). Targets the branches left uncovered at the 89.3% baseline:
// listTarBz2/listTarXz, rtf emitText/decodeCodePageByte, epub error ladder,
// extractHTMLFromZip fallbacks, pdf null/unreadable pages, odf/iWork/office
// error branches, nested-archive depth handling, and archive truncation
// markers.

// ---- helpers -----------------------------------------------------------

// sa139BuildZip builds a zip archive in memory.
func sa139BuildZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// sa139BuildTar builds a plain tar archive in memory.
func sa139BuildTar(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for name, data := range files {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

// sa139BuildPDF assembles a minimal but structurally valid PDF with a classic
// xref table (byte offsets computed at runtime). Each entry of objs is the
// body of object i+1 (without the "i 0 obj"/"endobj" wrapper). Streams must
// carry their own dict + stream/.../endstream.
func sa139BuildPDF(t *testing.T, objs []string) []byte {
	t.Helper()
	var out bytes.Buffer
	offsets := make([]int, len(objs)+1) // 1-based object numbers
	out.WriteString("%PDF-1.4\n")
	for i, body := range objs {
		offsets[i+1] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xrefOff := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", len(objs)+1)
	out.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xrefOff)
	return out.Bytes()
}

// sa139PDFStream renders a stream object body with exact /Length.
func sa139PDFStream(dictExtra string, payload string) string {
	return fmt.Sprintf("<< /Length %d %s>>\nstream\n%sendstream", len(payload), dictExtra, payload)
}

// ---- PDF ---------------------------------------------------------------

func TestSA139_PdfOpenError(t *testing.T) {
	_, err := (pdfExtractor{}).Extract([]byte("this is definitely not a pdf document"))
	if err == nil {
		t.Fatal("expected error for non-PDF bytes")
	}
	if !strings.Contains(err.Error(), "open PDF") {
		t.Fatalf("want wrapped open error, got: %v", err)
	}
}

func TestSA139_PdfNullPageMarker(t *testing.T) {
	// Kids points at object 9 which does not exist: Page(1) must come back
	// with a null V and the extractor must emit the #1729 empty-page marker.
	pdf := sa139BuildPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [9 0 R] /Count 1 >>",
	})
	res, err := (pdfExtractor{}).Extract(pdf)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.Pages != 1 {
		t.Fatalf("Pages = %d, want 1", res.Pages)
	}
	if !strings.Contains(res.Text, "[page 1 empty: page object missing]") {
		t.Fatalf("missing null-page marker, got: %q", res.Text)
	}
}

func TestSA139_PdfPageVariants(t *testing.T) {
	// Page 1: healthy with content. Page 2: no /Contents (empty text).
	// Page 3: /Contents is a stream with a broken FlateDecode payload.
	pdf := sa139BuildPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R 5 0 R] /Count 3 >>",
		"<< /Type /Page /Parent 2 0 R /Contents 6 0 R /Resources << /Font << /F1 8 0 R >> >> >>",
		"<< /Type /Page /Parent 2 0 R >>",
		"<< /Type /Page /Parent 2 0 R /Contents 7 0 R /Resources << /Font << /F1 8 0 R >> >> >>",
		sa139PDFStream("", "BT /F1 12 Tf 72 720 Td (Hello Page1) Tj ET"),
		sa139PDFStream("/Filter /FlateDecode ", "GARBAGE-not-flate"),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
	res, err := (pdfExtractor{}).Extract(pdf)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.Pages != 3 {
		t.Fatalf("Pages = %d, want 3", res.Pages)
	}
	if !strings.Contains(res.Text, "Hello Page1") {
		t.Fatalf("page 1 text missing, got: %q", res.Text)
	}
	// Page 2 has no Contents: GetPlainText returns "" and nothing is emitted
	// for it (the silent-empty continue branch).
	if strings.Contains(res.Text, "page 2") {
		t.Fatalf("page 2 should vanish silently (empty text), got: %q", res.Text)
	}
	// Page 3's stream cannot inflate: the #1542 unreadable-page marker must
	// appear with the correct 1-based page number (#1729).
	if !strings.Contains(res.Text, "[page 3 unreadable:") {
		t.Fatalf("missing unreadable-page marker for broken flate stream, got: %q", res.Text)
	}
}

// ---- RTF ---------------------------------------------------------------

func TestSA139_RTFEscapesEmitText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"tilde hyphen underscore", `{\rtf1 A\~B\-C\_D\*E}`, "A B-C-DE"},
		{"backslash newline is paragraph break", "{\\rtf1 line1\\\nline2}", "line1\nline2"},
		{"escapes suppressed inside fonttbl", "{\\rtf1{\\fonttbl A~B}body}", "body"},
	}
	for _, tc := range cases {
		res, err := (rtfExtractor{}).Extract([]byte(tc.in))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if res.Text != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, res.Text, tc.want)
		}
	}
}

func TestSA139_DecodeCodePageByte(t *testing.T) {
	cases := []struct {
		cp   int
		b    byte
		want rune
	}{
		{1252, 0x80, 0x20AC},  // Euro sign
		{0, 0xE9, 0xE9},       // cp 0 defaults to 1252: e-acute
		{99999, 0x93, 0x201C}, // unknown cp falls back to 1252: left smart quote
		{1251, 0xE0, 0x0430},  // Cyrillic a
		{1255, 0xE0, 0x05D0},  // Hebrew alef
		{932, 0x82, 0x82},     // CJK cp: raw byte passthrough
		{936, 0xFF, 0xFF},     // CJK cp: raw byte passthrough
	}
	for _, tc := range cases {
		if got := decodeCodePageByte(tc.cp, tc.b); got != tc.want {
			t.Fatalf("decodeCodePageByte(%d, %#x) = %#U, want %#U", tc.cp, tc.b, got, tc.want)
		}
	}
	// Remaining single-byte code pages: each switch arm must route to its
	// own x/text charmap (mapping values themselves are owned by x/text).
	for _, tc := range []struct {
		cp int
		cm *charmap.Charmap
	}{
		{1250, charmap.Windows1250}, {1253, charmap.Windows1253},
		{1254, charmap.Windows1254}, {1256, charmap.Windows1256},
		{1257, charmap.Windows1257}, {1258, charmap.Windows1258},
	} {
		if got := decodeCodePageByte(tc.cp, 0xE0); got != tc.cm.DecodeByte(0xE0) {
			t.Fatalf("cp %d byte 0xE0: got %#U want %#U", tc.cp, got, tc.cm.DecodeByte(0xE0))
		}
	}

	// Via real RTF bytes: \ansicpg selects the code page for \'XX escapes.
	res, err := (rtfExtractor{}).Extract([]byte(`{\rtf1\ansicpg1251\'e0}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "\u0430" {
		t.Fatalf("cp1251 escape: got %q want Cyrillic a", res.Text)
	}
	// Missing \ansicpg numeric param is ignored -> 1252 semantics.
	res, err = (rtfExtractor{}).Extract([]byte(`{\rtf1\ansicpg\'e9}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "é" {
		t.Fatalf("missing anscpg param: got %q want e-acute", res.Text)
	}
	// CJK code page: raw byte passthrough.
	res, err = (rtfExtractor{}).Extract([]byte("{\\rtf1\\ansicpg932\\'82}"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != string(rune(0x82)) {
		t.Fatalf("cp932 passthrough: got %q", res.Text)
	}
}

func TestSA139_RTFSurrogatesAndBin(t *testing.T) {
	// Two consecutive high surrogates: first becomes U+FFFD, second stays
	// pending and is flushed as U+FFFD when plain text follows.
	res, err := (rtfExtractor{}).Extract([]byte(`{\rtf1 \u55357\u55357X}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "\uFFFD\uFFFDX" {
		t.Fatalf("high-high: got %q", res.Text)
	}
	// Unpaired low surrogate -> U+FFFD.
	res, err = (rtfExtractor{}).Extract([]byte(`{\rtf1 \u56320X}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Text, "\uFFFD") {
		t.Fatalf("unpaired low: got %q", res.Text)
	}
	// \uN inside a skipped destination must not reach the buffer.
	res, err = (rtfExtractor{}).Extract([]byte(`{\rtf1{\fonttbl \u65 }ok}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "ok" {
		t.Fatalf("skip-destination \\u: got %q", res.Text)
	}
	// \binN: trailing delimiter space is syntax, N raw bytes are skipped.
	res, err = (rtfExtractor{}).Extract([]byte(`{\rtf1 A\bin4 XXXXB}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "AB" {
		t.Fatalf("\\bin4: got %q want AB", res.Text)
	}
	// \bin0 is ignored (v > 0 required).
	res, err = (rtfExtractor{}).Extract([]byte(`{\rtf1 A\bin0 B}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "AB" {
		t.Fatalf("\\bin0: got %q want AB", res.Text)
	}
	// \binN running past end of input: clamped, no panic.
	res, err = (rtfExtractor{}).Extract([]byte(`{\rtf1 A\bin99 BB}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "A" {
		t.Fatalf("\\bin99 clamp: got %q want A", res.Text)
	}
}

// ---- ODF ----------------------------------------------------------------

func TestSA139_ExtractXMLTextMatrix(t *testing.T) {
	in := "<d><p>a<tab/>b</p>" +
		"<table-row><table-cell>c1</table-cell><table-cell>c2</table-cell></table-row>" +
		"<h>Head</h><page-break/>after-break<br/>x<line-break/>y" +
		"   <p>   </p>" + // whitespace-only CharData is dropped
		"<<<" // malformed tail forces the decoder-error break
	got := extractXMLText(in)
	for _, want := range []string{"a\tb", "c1\tc2", "Head", "\n---\n", "after-break"} {
		if !strings.Contains(got, want) {
			t.Fatalf("extractXMLText missing %q in %q", want, got)
		}
	}
	if strings.Count(got, "\t") < 2 {
		t.Fatalf("expected cell/tab markers, got %q", got)
	}
}

func TestSA139_ODFBranches(t *testing.T) {
	if _, err := (&odfExtractor{subFormat: "odt"}).Extract([]byte("not a zip")); err == nil || !strings.Contains(err.Error(), "open ODF archive") {
		t.Fatalf("garbage input: %v", err)
	}
	noContent := sa139BuildZip(t, map[string][]byte{"mimetype": []byte("application/vnd.oasis.opendocument.text")})
	if _, err := (&odfExtractor{subFormat: "odt"}).Extract(noContent); err == nil || !strings.Contains(err.Error(), "missing content.xml") {
		t.Fatalf("missing content.xml: %v", err)
	}
	content := []byte("<office:document-content><text:p>Hello<text:page-break/>World</text:p></office:document-content>")
	doc := sa139BuildZip(t, map[string][]byte{"content.xml": content})
	res, err := (&odfExtractor{subFormat: "odt"}).Extract(doc)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 2 {
		t.Fatalf("odt Pages = %d, want 2 (one page-break)", res.Pages)
	}
	if res.Format != "odt" || !strings.Contains(res.Text, "Hello") || !strings.Contains(res.Text, "World") {
		t.Fatalf("odt text: %+v", res)
	}
	// Non-odt subFormat does not compute page counts.
	res, err = (&odfExtractor{subFormat: "ods"}).Extract(doc)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 0 {
		t.Fatalf("ods Pages = %d, want 0", res.Pages)
	}
}

// ---- Office (docx/xlsx/pptx) --------------------------------------------

func TestSA139_OfficeOpenErrors(t *testing.T) {
	cases := []struct {
		name string
		e    Extractor
		want string
	}{
		{"docx", docxExtractor{}, "open DOCX"},
		{"xlsx", xlsxExtractor{}, "open XLSX"},
		{"pptx", pptxExtractor{}, "open PPTX"},
	}
	for _, tc := range cases {
		if _, err := tc.e.Extract([]byte("not a zip")); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s garbage input: %v", tc.name, err)
		}
	}
	// A zip that opens but carries no document parts: oxmltotext only logs a
	// warning and returns empty text with a nil error - pin that tolerant
	// behavior so a future hard-error change is a conscious one.
	empty := sa139BuildZip(t, map[string][]byte{"readme.txt": []byte("nothing office-ish here")})
	res, err := (docxExtractor{}).Extract(empty)
	if err != nil {
		t.Fatalf("docx with no word/ parts: unexpected error: %v", err)
	}
	if res.Text != "" {
		t.Fatalf("docx with no word/ parts: got %q, want empty", res.Text)
	}
}

// ---- iWork ---------------------------------------------------------------

func TestSA139_IWorkBranches(t *testing.T) {
	if _, err := (&iworkExtractor{subFormat: "pages"}).Extract([]byte("not a zip")); err == nil || !strings.Contains(err.Error(), "open pages archive") {
		t.Fatalf("garbage input: %v", err)
	}
	iwaOnly := sa139BuildZip(t, map[string][]byte{"Index.iwa": {0x00, 0x01, 0x02}})
	if _, err := (&iworkExtractor{subFormat: "pages"}).Extract(iwaOnly); err == nil || !strings.Contains(err.Error(), "no .xml parts") {
		t.Fatalf("iwa-only: %v", err)
	}
	emptyXML := sa139BuildZip(t, map[string][]byte{"Empty.xml": []byte("<r></r>")})
	if _, err := (&iworkExtractor{subFormat: "key"}).Extract(emptyXML); err == nil || !strings.Contains(err.Error(), "no extractable text") {
		t.Fatalf("empty xml: %v", err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	dir, err := w.Create("Archive/") // dir entry is skipped
	if err != nil {
		t.Fatal(err)
	}
	dir.Write(nil)
	doc, err := w.Create("Doc.xml")
	if err != nil {
		t.Fatal(err)
	}
	doc.Write([]byte("<r><p>Hello iWork</p></r>"))
	w.Close()
	res, err := (&iworkExtractor{subFormat: "key"}).Extract(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "key" || !strings.Contains(res.Text, "Hello iWork") {
		t.Fatalf("iwork success path: %+v", res)
	}
}

// ---- EPUB ----------------------------------------------------------------

func sa139OPF(spineIDRefs []string, manifestItems []string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><package><manifest>`)
	for _, m := range manifestItems {
		b.WriteString(m)
	}
	b.WriteString(`</manifest><spine>`)
	for _, id := range spineIDRefs {
		fmt.Fprintf(&b, `<itemref idref="%s"/>`, id)
	}
	b.WriteString(`</spine></package>`)
	return []byte(b.String())
}

func sa139Container(opfPath string) []byte {
	if opfPath == "" {
		return []byte(`<?xml version="1.0"?><container><rootfile/></container>`)
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0"?><container><rootfile full-path="%s"/></container>`, opfPath))
}

func TestSA139_EpubErrorLadder(t *testing.T) {
	if _, err := (epubExtractor{}).Extract([]byte("not a zip")); err == nil || !strings.Contains(err.Error(), "open EPUB archive") {
		t.Fatalf("garbage: %v", err)
	}
	// No container.xml at all.
	noContainer := sa139BuildZip(t, map[string][]byte{"mimetype": []byte("application/epub+zip")})
	if _, err := (epubExtractor{}).Extract(noContainer); err == nil || !strings.Contains(err.Error(), "missing META-INF/container.xml") {
		t.Fatalf("no container: %v", err)
	}
	// container.xml present but rootfile lacks full-path.
	rootless := sa139BuildZip(t, map[string][]byte{"META-INF/container.xml": sa139Container("")})
	if _, err := (epubExtractor{}).Extract(rootless); err == nil || !strings.Contains(err.Error(), "missing META-INF/container.xml or rootfile") {
		t.Fatalf("rootless container: %v", err)
	}
	// container points at an OPF that is not in the archive.
	opfMissing := sa139BuildZip(t, map[string][]byte{"META-INF/container.xml": sa139Container("content.opf")})
	if _, err := (epubExtractor{}).Extract(opfMissing); err == nil || !strings.Contains(err.Error(), "OPF file not found: content.opf") {
		t.Fatalf("opf missing: %v", err)
	}
	// OPF exists but spine has no itemrefs.
	emptySpine := sa139BuildZip(t, map[string][]byte{
		"META-INF/container.xml": sa139Container("content.opf"),
		"content.opf":            sa139OPF(nil, nil),
	})
	if _, err := (epubExtractor{}).Extract(emptySpine); err == nil || !strings.Contains(err.Error(), "empty spine") {
		t.Fatalf("empty spine: %v", err)
	}
	// Spine idrefs that resolve to nothing in the manifest also yield an
	// empty spine list.
	dangling := sa139BuildZip(t, map[string][]byte{
		"META-INF/container.xml": sa139Container("content.opf"),
		"content.opf":            sa139OPF([]string{"ghost"}, []string{`<item id="real" href="chap.xhtml"/>`}),
	})
	if _, err := (epubExtractor{}).Extract(dangling); err == nil || !strings.Contains(err.Error(), "empty spine") {
		t.Fatalf("dangling idref: %v", err)
	}
	// Spine lists a chapter that does not exist in the zip: every item is
	// "missing" and the #566(G) diagnostic fires.
	missing := sa139BuildZip(t, map[string][]byte{
		"META-INF/container.xml": sa139Container("content.opf"),
		"content.opf":            sa139OPF([]string{"c1"}, []string{`<item id="c1" href="missing.xhtml"/>`}),
	})
	_, err := (epubExtractor{}).Extract(missing)
	if err == nil || !strings.Contains(err.Error(), "none exist in the archive") {
		t.Fatalf("missing chapter: %v", err)
	}
	// One chapter unreadable, one real: extraction succeeds and the real
	// chapter's text wins.
	partial := sa139BuildZip(t, map[string][]byte{
		"META-INF/container.xml": sa139Container("content.opf"),
		"content.opf":            sa139OPF([]string{"c1", "c2"}, []string{`<item id="c1" href="gone.xhtml"/>`, `<item id="c2" href="text/chap.xhtml"/>`}),
		"text/chap.xhtml":        []byte("<html><body><p>Real Chapter</p></body></html>"),
	})
	res, err := (epubExtractor{}).Extract(partial)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 1 || !strings.Contains(res.Text, "Real Chapter") {
		t.Fatalf("partial spine: %+v", res)
	}
}

func TestSA139_EpubCaseInsensitive(t *testing.T) {
	// OPF references chap1.xhtml, the zip stores the uppercase variant:
	// the case-insensitive fallback loop must find it.
	epub := sa139BuildZip(t, map[string][]byte{
		"META-INF/container.xml": sa139Container("content.opf"),
		"content.opf":            sa139OPF([]string{"c1"}, []string{`<item id="c1" href="chap1.xhtml"/>`}),
		"CHAP1.XHTML":            []byte("<html><body><p>CaseInsensitive Hit</p></body></html>"),
	})
	res, err := (epubExtractor{}).Extract(epub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "CaseInsensitive Hit") {
		t.Fatalf("case-insensitive fallback: %+v", res)
	}
}

func TestSA139_ExtractHTMLTextMatrix(t *testing.T) {
	in := `<html><head><title>TitleSecretWord</title><style>.css{display:none}</style>` +
		`<script>var ScriptSecretWord = 1;</script></head>` +
		`<body><p>One</p><div>Two<br/>2b</div><ul><li>L1</li></ul>` +
		`<table><tr><td>C1</td></tr></table></body></html>`
	got := extractHTMLText(in)
	for _, want := range []string{"One", "Two", "2b", "L1", "C1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("extractHTMLText missing %q in %q", want, got)
		}
	}
	for _, banned := range []string{"TitleSecretWord", "ScriptSecretWord", ".css{display:none}"} {
		if strings.Contains(got, banned) {
			t.Fatalf("extractHTMLText leaked %q in %q", banned, got)
		}
	}
}

// ---- Archives ------------------------------------------------------------

func TestSA139_ArchiveUnsupportedAndZipCounters(t *testing.T) {
	if _, err := (&archiveExtractor{subFormat: "rar"}).Extract(nil); err == nil || !strings.Contains(err.Error(), "unsupported archive format: rar") {
		t.Fatalf("rar subFormat: %v", err)
	}
	if got := totalZipFiles([]byte("garbage-not-a-zip")); got != 0 {
		t.Fatalf("totalZipFiles(garbage) = %d, want 0", got)
	}
}

func TestSA139_TarBz2HappyAndCorrupt(t *testing.T) {
	bz2, err := os.ReadFile(filepath.Join("testdata", "sample.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Extract("docs.tar.bz2", bz2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "tar.bz2" {
		t.Fatalf("Format = %q", res.Format)
	}
	for _, want := range []string{"[Archive: tar.bz2 format, 2 files]", "--- hello.txt", "hello from bz2 archive", "--- notes.txt"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("tar.bz2 output missing %q in %q", want, res.Text)
		}
	}
	// Corrupt bzip2 stream: the #687 corruption marker, not a size limit.
	res, err = Extract("bad.tar.bz2", []byte("this is not bzip2 data at all"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "[Corrupt archive: stream error after") {
		t.Fatalf("corrupt bz2: %q", res.Text)
	}
}

func TestSA139_TarXzHonestUnsupported(t *testing.T) {
	// #547 contract: .tar.xz is NOT registered and Extract reports the gap
	// up front instead of pretending support.
	if IsDocumentFile("book.tar.xz") {
		t.Fatal(".tar.xz must not be registered (#547)")
	}
	if _, err := (&archiveExtractor{subFormat: "tar.xz"}).Extract([]byte("garbage")); err == nil || !strings.Contains(err.Error(), "tar.xz support requires xz decompression") {
		t.Fatalf("tar.xz: %v", err)
	}
}

func TestSA139_ZipPreviewTruncationRuneSnap(t *testing.T) {
	// 70000 bytes of two-byte runes: the 64KB preview cut lands mid-rune and
	// must snap back to a boundary before appending the marker.
	words := bytes.Repeat([]byte("é"), 35000)
	z := sa139BuildZip(t, map[string][]byte{"big.txt": words})
	res, err := Extract("big.zip", z)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "[truncated at 65536 bytes]") {
		t.Fatalf("missing preview truncation marker: %q", res.Text)
	}
	if !utf8.ValidString(res.Text) {
		t.Fatal("truncation sliced a multi-byte rune")
	}
}

func TestSA139_ZipBinaryAndDotSlashEntries(t *testing.T) {
	z := sa139BuildZip(t, map[string][]byte{
		"pic.png":   []byte("fake-png-bytes"),
		"prog.exe":  []byte("MZ-fake-binary"),
		"./rel.txt": []byte("relative entry"),
	})
	res, err := Extract("mixed.zip", z)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(res.Text, "[Binary: skipped]"); got < 2 {
		t.Fatalf("expected 2 binary skips, got %d in %q", got, res.Text)
	}
	if !strings.Contains(res.Text, "--- rel.txt (") { // "./" prefix trimmed
		t.Fatalf("./ prefix not trimmed: %q", res.Text)
	}
}

func TestSA139_ZipEntryCountTruncation(t *testing.T) {
	files := make(map[string][]byte)
	for i := 0; i < 501; i++ {
		files[fmt.Sprintf("f%03d.txt", i)] = []byte(fmt.Sprintf("body %d", i))
	}
	res, err := Extract("many.zip", sa139BuildZip(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "[Showing first 500 of 501 files]") {
		t.Fatalf("zip entry-cap marker missing: %q", res.Text)
	}
}

func TestSA139_TarEntryCountTruncation(t *testing.T) {
	// >maxArchiveEntries regular files: the drain loop must keep counting and
	// the listing must say "first 500 of N".
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for i := 0; i < 501; i++ {
		body := []byte(fmt.Sprintf("body %d", i))
		if err := w.WriteHeader(&tar.Header{Name: fmt.Sprintf("f%03d.txt", i), Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	res, err := Extract("many.tar", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "[Showing first 500 of 501 files]") {
		t.Fatalf("tar drain marker missing: %q", res.Text)
	}
}

func TestSA139_TarEntryTooLarge(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 1200*1024) // 1.2MB > maxArchiveEntrySize
	res, err := Extract("big.tar", sa139BuildTar(t, map[string][]byte{"huge.txt": big}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "[File too large: 1.0MB]") {
		t.Fatalf("file-too-large marker missing: %q", res.Text)
	}
}

func TestSA139_TarGzCorruptMarker(t *testing.T) {
	// Single 300-byte entry: 512-byte header + 512-byte data block, then the
	// two zero end-of-archive blocks (total 2048). Cutting at 1124 keeps the
	// entry intact but leaves only 100 bytes of the next block: archive/tar
	// reports ErrUnexpectedEOF on the header read, which listTarFromReader
	// must classify as corruption (#687), not a size limit.
	body := bytes.Repeat([]byte("a"), 300)
	full := sa139BuildTar(t, map[string][]byte{"only.txt": body})
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	gw.Write(full[:1124])
	gw.Close()
	res, err := Extract("broken.tar.gz", gz.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "[Corrupt archive: stream error after") {
		t.Fatalf("corrupt tar.gz marker missing: %q", res.Text)
	}
}

func TestSA139_TarShortEntryUnreadable(t *testing.T) {
	// Hand-rolled ustar header declaring 1200 bytes when only 1034 remain
	// after the header: the entry read hits EOF mid-declaration and #1539
	// keeps the entry visible with a marker.
	hdr := make([]byte, 512)
	copy(hdr, "short.txt")
	copy(hdr[100:], "0000644\x00")
	copy(hdr[108:], "0000000\x00")
	copy(hdr[116:], "0000000\x00")
	copy(hdr[124:], "00000002260\x00") // size 1200 decimal = 02260 octal (> bytes present)
	copy(hdr[136:], "00000000000\x00")
	hdr[156] = '0' // regular file
	copy(hdr[257:], "ustar\x0000")
	sum := 0
	for i, b := range hdr {
		if i >= 148 && i < 156 {
			sum += ' '
			continue
		}
		sum += int(b)
	}
	copy(hdr[148:], fmt.Sprintf("%06o\x00 ", sum))

	var buf bytes.Buffer
	buf.Write(hdr)
	buf.WriteString("shortonly!") // 10 bytes, not 100, no padding
	buf.Write(make([]byte, 1024)) // end-of-archive blocks
	res, err := Extract("short.tar", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "[unreadable:") {
		t.Fatalf("unreadable marker missing: %q", res.Text)
	}
}

func TestSA139_NestedArchives(t *testing.T) {
	inner := sa139BuildZip(t, map[string][]byte{"inner.txt": []byte("inner zip body")})
	outer := sa139BuildZip(t, map[string][]byte{
		"inner.zip":      inner,
		"mystery.tar.xz": []byte("not really xz"),
		"blob.rar":       []byte("not really rar"),
	})
	res, err := Extract("outer.zip", outer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "inner zip body") {
		t.Fatalf("nested zip text missing: %q", res.Text)
	}
	for _, want := range []string{"[Unsupported nested archive format: .tar.xz]", "[Unsupported nested archive format: .rar]"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("missing %q in %q", want, res.Text)
		}
	}
}

func TestSA139_ExtractArchiveContentDepthDirect(t *testing.T) {
	if got := extractArchiveContentDepth(nil, ".7z", 0); got != "[Unsupported nested archive format: .7z]" {
		t.Fatalf("7z: %q", got)
	}
	if got := extractArchiveContentDepth(nil, ".weird", 0); got != "" {
		t.Fatalf("unknown ext: %q", got)
	}
	if got := extractArchiveContentDepth(nil, ".zip", maxArchiveDepth); got != "" {
		t.Fatalf("depth limit: %q", got)
	}
	corrupt := []byte("PK\x03\x04corrupted-payload")
	if got := extractArchiveContentDepth(corrupt, ".zip", 1); !strings.Contains(got, "[Extraction failed:") {
		t.Fatalf("corrupt nested zip: %q", got)
	}
	valid := sa139BuildZip(t, map[string][]byte{"ok.txt": []byte("depth ok")})
	if got := extractArchiveContentDepth(valid, ".zip", 1); !strings.Contains(got, "depth ok") {
		t.Fatalf("valid nested zip: %q", got)
	}
}

func TestSA139_ArchiveBufferOverflowMarker(t *testing.T) {
	files := make(map[string][]byte)
	for i := 0; i < 4; i++ {
		files[fmt.Sprintf("pad%d.txt", i)] = bytes.Repeat([]byte("x"), 55*1024)
	}
	res, err := Extract("wide.zip", sa139BuildZip(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "... (truncated, too many files)") {
		t.Fatalf("buffer overflow marker missing (tail): %q", res.Text[len(res.Text)-200:])
	}
}

// ---- Registry / extOf -----------------------------------------------------

func TestSA139_ExtOfMatrix(t *testing.T) {
	cases := map[string]string{
		"a.TAR.BZ2":  ".tar.bz2",
		"x.tar.xz":   ".tar.xz",
		"y.tar.gz":   ".tar.gz",
		"n.TgZ":      ".tgz",
		"plain.txt":  ".txt",
		"noext":      "",
		"dir.tar.gz": ".tar.gz",
	}
	for in, want := range cases {
		if got := extOf(in); got != want {
			t.Fatalf("extOf(%q) = %q, want %q", in, got, want)
		}
	}
}
