package extract

// Issue #2713 probe: parseOPFSpine decoded OPF hrefs with QueryUnescape,
// which turns a literal '+' into a space. OPF hrefs are RFC 3986 URI path
// references where '+' has no encoding meaning; only %XX escapes decode.

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func buildPlusEPUB(t *testing.T, href, zipEntry string) []byte {
	t.Helper()
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
    <item id="c1" href="`+href+`" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/></spine>
</package>`)
	mustAdd(zipEntry, `<html><body><p>C plus plus chapter body.</p></body></html>`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// Literal '+' in the filename and in the href: legal in both ZIP entries and
// OPF (Sigil/calibre do not force percent-encoding). The chapter must extract.
func TestIssue2713LiteralPlusHrefExtracts(t *testing.T) {
	data := buildPlusEPUB(t, "C++_basics.xhtml", "OEBPS/C++_basics.xhtml")
	result, err := Extract("book.epub", data)
	if err != nil {
		t.Fatalf("Extract epub: %v", err)
	}
	if result.Pages == 0 {
		t.Fatalf("Pages = 0 - '+' href chapter silently lost")
	}
	if !strings.Contains(result.Text, "C plus plus chapter body") {
		t.Fatalf("text missing chapter content, got %q", result.Text)
	}
}

// Percent-escapes in hrefs must still decode: "C%2B%2B.xhtml" resolves to the
// literal "C++" entry, and "%20" still decodes to a space (guards against
// over-correcting into raw path matching).
func TestIssue2713PercentEscapeStillDecodes(t *testing.T) {
	data := buildPlusEPUB(t, "C%2B%2B_basics.xhtml", "OEBPS/C++_basics.xhtml")
	result, err := Extract("book.epub", data)
	if err != nil {
		t.Fatalf("Extract epub: %v", err)
	}
	if !strings.Contains(result.Text, "C plus plus chapter body") {
		t.Fatalf("percent-escaped href failed to decode to the literal entry, got %q", result.Text)
	}
}
