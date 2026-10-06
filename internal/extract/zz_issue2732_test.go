package extract

// Issue #2732 probe: the XML decoder token loops in findOPFPath and
// parseOPFSpine broke on ANY error (including real syntax/malformation
// errors) and then reported a misleading "missing rootfile" / "empty
// spine" verdict. Real parse failures must surface with context.

import (
	"strings"
	"testing"
)

// Scenario A: container.xml truncated mid-tag BEFORE rootfile -> the real
// problem is a syntax error, not a missing file.
func TestIssue2732TruncatedContainerReportsParseError(t *testing.T) {
	epub := buildMinimalEPUB(t, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container version="1.0"><rootfiles><ro`,
	})
	_, err := Extract("book.epub", epub)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "container.xml") || !strings.Contains(err.Error(), "XML") {
		t.Fatalf("misleading error masks parse failure: %q", err.Error())
	}
}

// Scenario B: OPF manifest contains an illegal raw '<' BEFORE the spine ->
// the real problem is a syntax error, not an empty spine.
func TestIssue2732CorruptOPFReportsParseError(t *testing.T) {
	epub := buildMinimalEPUB(t, map[string]string{
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <manifest><item id="c1" href="chap1.xhtml" media-type="application/xhtml+xml"/><broken
  <spine><itemref idref="c1"/></spine>
</package>`,
		"OEBPS/chap1.xhtml": `<html><body><p>body</p></body></html>`,
	})
	_, err := Extract("book.epub", epub)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if strings.Contains(err.Error(), "empty spine") {
		t.Fatalf("misleading empty-spine verdict masks OPF parse failure: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "XML") && !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error lacks parse context: %q", err.Error())
	}
}
