package extract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// Regression for #1539: >64KB plain-text zip entries were silently
// truncated to content that looked complete (no marker).
func TestZipTextEntryTruncationMarked(t *testing.T) {
	big := strings.Repeat("x", 100*1024) // 100KB text, over the 64KB probe limit
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("big.txt")
	f.Write([]byte(big))
	w.Close()

	result, err := Extract("big.zip", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "[truncated at") {
		t.Fatalf("truncated entry must carry an honest marker; tail: %q", tailOf(result.Text, 80))
	}
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// Regression for #1540: macro-enabled OOXML variants (.docm/.xlsm/.pptm)
// are the same zip format and must be routed to the extractors, not fall
// through to raw binary content.
func TestMacroOfficeExtensionsRegistered(t *testing.T) {
	for _, ext := range []string{".docm", ".xlsm", ".pptm"} {
		if defaultRegistry.Get(ext) == nil {
			t.Errorf("extension %s must be registered", ext)
		}
	}
	if !IsDocumentFile("report.docm") {
		t.Error(".docm must be recognized as a document file")
	}
}
