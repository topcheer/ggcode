package main

import (
	"mime"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture writes content to name inside a fresh temp dir.
func writeFixture(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writing fixture %s: %v", name, err)
	}
	return path
}

// TestIssue675_SVGKeepsExtensionMime: a pasted .svg is UTF-8 text (it fails
// imgpkg.ReadFile's binary sniff), so it takes the text branch — but its
// extension mime (image/svg+xml) must be preserved. #671's text/-only
// whitelist degraded it to the sniffed text/plain, contradicting the
// comment's "richer text type" intent (#675).
func TestIssue675_SVGKeepsExtensionMime(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="red"/></svg>`)
	path := writeFixture(t, "icon.svg", svg)

	att := readClipboardFileAttachment(path)
	if att.Error != "" {
		t.Fatalf("svg paste must not error: %s", att.Error)
	}
	if att.Kind != "text" {
		t.Fatalf("svg paste must be Kind=text, got %q", att.Kind)
	}
	want := mime.TypeByExtension(".svg")
	if want != "image/svg+xml" {
		t.Skipf("system mime table does not map .svg to image/svg+xml (got %q); cannot exercise the regression here", want)
	}
	if att.MimeType != "image/svg+xml" {
		t.Fatalf("#675: .svg must keep image/svg+xml, degraded to %q", att.MimeType)
	}
}

// TestIssue675_XMLFamilyExtensionMimesPreserved: textual extension mimes
// outside text/ (XML family + textual application/*) must be kept in the
// text branch whenever isTextualExtensionMime(extMime) is true — i.e. the
// kept/kept-not decision must match the helper exactly — while content that
// is not text still goes to the binary rejection path.
func TestIssue675_XMLFamilyExtensionMimesPreserved(t *testing.T) {
	cases := []struct {
		ext     string
		content []byte
	}{
		{".svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		{".xml", []byte(`<?xml version="1.0"?><root/>`)},
		{".json", []byte(`{"a": 1}`)},
		{".csv", []byte("a,b\n1,2\n")},
		{".txt", []byte("plain hello\n")},
		{".yaml", []byte("a: 1\n")},
		// Binary-ish application mime does NOT qualify; sniffed value wins.
		// (.png with textual content: sniff=text/plain ≠ image/png, so a kept
		// extension mime would be observable.)
		{".png", []byte("this is really just plain text, not a png image\n")},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			path := writeFixture(t, "f"+tc.ext, tc.content)
			att := readClipboardFileAttachment(path)
			if att.Kind != "text" || att.Error != "" {
				t.Fatalf("%s content must classify as clean text, got kind=%q err=%q", tc.ext, att.Kind, att.Error)
			}
			extMime := mime.TypeByExtension(tc.ext)
			qualifies := extMime != "" && isTextualExtensionMime(extMime)
			if qualifies {
				if att.MimeType != extMime {
					t.Fatalf("%s: textual extension mime %q must be kept, got %q", tc.ext, extMime, att.MimeType)
				}
			} else {
				if extMime != "" && att.MimeType == extMime {
					t.Fatalf("%s: non-textual extension mime %q must NOT be kept in the text branch", tc.ext, extMime)
				}
				if !strings.HasPrefix(att.MimeType, "text/") {
					t.Fatalf("%s: fallback mime must be the sniffed text/* value, got %q", tc.ext, att.MimeType)
				}
			}
		})
	}
}

// TestIssue675_IsTextualExtensionMime pins the whitelist semantics: text/*,
// any RFC 7303 …+xml suffix (image/svg+xml being the #675 case), and the
// common textual application/* types; everything else is not textual.
func TestIssue675_IsTextualExtensionMime(t *testing.T) {
	for _, m := range []string{
		"text/plain", "text/csv", "text/html; charset=utf-8",
		"image/svg+xml", "application/xml", "text/xml", "application/rss+xml",
		"application/json", "application/javascript", "application/yaml",
		"application/x-yaml", "application/toml",
	} {
		if !isTextualExtensionMime(m) {
			t.Errorf("%q must be textual (#675)", m)
		}
	}
	for _, m := range []string{
		"image/png", "image/jpeg", "application/pdf", "application/zip",
		"application/octet-stream", "audio/mpeg", "video/mp4", "",
	} {
		if isTextualExtensionMime(m) {
			t.Errorf("%q must NOT be textual", m)
		}
	}
}
