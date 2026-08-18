package extract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"strings"
	"testing"
)

// buildTar builds a tar archive from name→content pairs.
func buildTar(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: e[0], Mode: 0o644, Size: int64(len(e[1])),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// zz682a: >500 entries → tar listing must carry a "[Showing first 500 of N]"
// truncation marker (zip path had it, tar path did not).
func TestZZ682_TarEntryCapMarksTruncation(t *testing.T) {
	var entries [][2]string
	for i := 0; i < 520; i++ {
		entries = append(entries, [2]string{fmt.Sprintf("f%03d.txt", i), "x"})
	}
	e := &archiveExtractor{subFormat: "tar"}
	res, err := e.Extract(buildTar(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "[Showing first 500 of 520 files]") {
		t.Errorf("missing truncation marker; head: %q", firstLine(res.Text, 3))
	}
	if !strings.Contains(res.Text, "[Archive: tar format, 500 files]") {
		t.Errorf("header should report 500 listed files: %q", firstLine(res.Text, 1))
	}
	// The truncation marker is what carries the total (zip-path semantics).
}

// zz682a: same for tar.gz.
func TestZZ682_TarGzEntryCapMarksTruncation(t *testing.T) {
	var entries [][2]string
	for i := 0; i < 505; i++ {
		entries = append(entries, [2]string{fmt.Sprintf("f%03d.txt", i), "x"})
	}
	var gzBuf bytes.Buffer
	zw := gzip.NewWriter(&gzBuf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e[0], Mode: 0o644, Size: int64(len(e[1]))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	zw.Close()

	e := &archiveExtractor{subFormat: "tar.gz"}
	res, err := e.Extract(gzBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "[Showing first 500 of 505 files]") {
		t.Errorf("tar.gz missing truncation marker; head: %q", firstLine(res.Text, 3))
	}
}

// zz682a: small archive — no marker, total == file count.
func TestZZ682_TarNoFalseTruncationMarker(t *testing.T) {
	e := &archiveExtractor{subFormat: "tar"}
	res, err := e.Extract(buildTar(t, [][2]string{{"a.txt", "hello"}, {"b.txt", "world"}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "[Showing first") || strings.Contains(res.Text, "[Truncated") {
		t.Errorf("false truncation marker on complete archive: %q", firstLine(res.Text, 3))
	}
	if !strings.Contains(res.Text, "[Archive: tar format, 2 files]") {
		t.Errorf("header should report 2 files: %q", firstLine(res.Text, 1))
	}
}

// zz682b: SVG decode error must surface — updated by #686: the original
// hard-error path dropped ALL extracted text ("partial text flagged" was
// promised but never shipped). The contract is now: partial text is
// returned AND explicitly flagged, never silently success-looking.
func TestZZ682_SVGDecodeErrorPropagates(t *testing.T) {
	bad := `<svg xmlns="http://www.w3.org/2000/svg"><text>good part</text> bad &entity <text>more</text></svg>`
	res, err := Extract("t.svg", []byte(bad))
	if err != nil {
		t.Fatalf("#686: decode errors must yield flagged partial text, not a hard error: %v", err)
	}
	if !strings.Contains(res.Text, "good part") {
		t.Errorf("partial text before the decode error must be kept: %q", res.Text)
	}
	if !strings.Contains(res.Text, "SVG decode error") {
		t.Errorf("partial text must be explicitly flagged, not silent success: %q", res.Text)
	}
}

// zz682b: script/style content must not leak into extracted text.
func TestZZ682_SVGScriptStyleExcluded(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">` +
		`<script>alert("evil()");</script>` +
		`<style>.a { fill: red; }</style>` +
		`<text>Hello SVG</text><title>Doc Title</title></svg>`
	res, err := Extract("t.svg", []byte(svg))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"alert", "evil", "fill", "red"} {
		if strings.Contains(res.Text, leak) {
			t.Errorf("script/style content leaked into text: %q", res.Text)
		}
	}
	if !strings.Contains(res.Text, "Hello SVG") || !strings.Contains(res.Text, "Doc Title") {
		t.Errorf("expected visible text preserved, got: %q", res.Text)
	}
}

// zz682b: aria-label on whitelisted elements still extracted.
func TestZZ682_SVGAriaLabelStillWorks(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><text aria-label="labeled">x</text></svg>`
	res, err := Extract("t.svg", []byte(svg))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "labeled") {
		t.Errorf("aria-label lost: %q", res.Text)
	}
}

func firstLine(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	return strings.Join(lines[:n], " | ")
}
