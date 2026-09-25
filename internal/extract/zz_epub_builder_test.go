package extract

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildMinimalEPUB assembles an EPUB with the default container/OPF unless
// overridden by files (nil values mean "omit this entry").
func buildMinimalEPUB(t *testing.T, overrides map[string]string) []byte {
	t.Helper()
	defaultFiles := map[string]string{
		"mimetype": "application/epub+zip",
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
  <manifest><item id="c1" href="chap1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`,
		"OEBPS/chap1.xhtml": `<html><body><p>chapter</p></body></html>`,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range defaultFiles {
		if override, ok := overrides[name]; ok {
			if override == "" {
				continue // omit
			}
			body = override
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	for name, body := range overrides {
		if _, isDefault := defaultFiles[name]; !isDefault {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatalf("create %s: %v", name, err)
			}
			if _, err := w.Write([]byte(body)); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
