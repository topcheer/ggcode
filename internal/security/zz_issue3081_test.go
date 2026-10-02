package security

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #3081 V1: C1 byte-level regexes must not corrupt UTF-8 continuation bytes.
func TestIssue3081TerminalEscapePreservesUTF8(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"cyrillic-La", "Лa"},             // D0 9B 61
		{"cyrillic-followed-digit", "Л2"}, // D0 9B 32
		{"latin-circumflex-U", "Ûb"},      // C3 9B 62
		{"emoji-grin", "\U0001F600!"},     // F0 9F 98 80
		{"russian-phrase", "Привет мир 123"},
	}
	for _, c := range cases {
		got := SanitizeTerminalForDisplay(c.in)
		if got != c.in {
			t.Errorf("%s: mangled %q -> %q", c.name, c.in, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: output invalid UTF-8: %q", c.name, got)
		}
	}
	// "Лa" is not an escape sequence at all: fast path returns it untouched.
	if SanitizeTerminalForDisplay("Лa") != "Лa" {
		t.Fatal("fast path corrupted plain Cyrillic text")
	}
}

// #3081 V1 companion: real standalone C1 sequences must still be stripped.
func TestIssue3081TerminalEscapeStillStripsC1(t *testing.T) {
	// raw 8-bit CSI: 0x9b 32 J == CSI 2 J (erase display)
	csi := "\x9b2Jhello"
	if got := SanitizeTerminalForDisplay(csi); got != "hello" {
		t.Errorf("C1 CSI 2J not stripped: %q", got)
	}
	// raw 8-bit OSC via 0x9d introducer, terminated by BEL
	osc := "\x9d0;title\x07body"
	if got := SanitizeTerminalForDisplay(osc); got != "body" {
		t.Errorf("C1 OSC not stripped: %q", got)
	}
	// 7-bit forms unaffected
	if got := SanitizeTerminalForDisplay("\x1b[2Jx"); got != "x" {
		t.Errorf("7-bit CSI regression: %q", got)
	}
	// mixed: standalone C1 CSI right before Cyrillic text must strip the
	// sequence but keep the Cyrillic intact
	mixed := "\x9b2JЛa"
	got := SanitizeTerminalForDisplay(mixed)
	if got != "Лa" {
		t.Errorf("mixed C1+Cyrillic: got %q want Лa", got)
	}
}

// #3081 V2: bare (truncated) PEM header must be masked at display time.
func TestIssue3081RedactTruncatedPEMHeader(t *testing.T) {
	truncated := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC"
	out := RedactForDisplay(truncated)
	if strings.Contains(out, "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC") {
		t.Errorf("truncated key body leaked: %q", out)
	}
	if strings.Contains(out, "-----BEGIN") {
		t.Errorf("bare header not masked: %q", out)
	}
	// complete block still handled by the original pattern
	complete := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----"
	out2 := RedactForDisplay(complete)
	if strings.Contains(out2, "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC") {
		t.Errorf("complete block body leaked: %q", out2)
	}
}

// #3081 V3: `.test.` real files must be scanned; fixture suffixes still allowlisted.
func TestIssue3081AllowlistNarrowing(t *testing.T) {
	const secretLine = "aws key: AKIAIOSFODNN7EXAMPLE"
	if ScanForSecrets("config/api.test.env", secretLine) == nil {
		t.Error("api.test.env skipped but is a real file")
	}
	if ScanForSecrets("deploy/prod.test.conf", secretLine) == nil {
		t.Error("prod.test.conf skipped but is a real file")
	}
	// fixture-style names remain allowlisted
	if ScanForSecrets("pkg/app.test.golden", secretLine) != nil {
		t.Error("fixture .test.golden should stay allowlisted")
	}
	if ScanForSecrets("bin/ggcode.test", secretLine) != nil {
		t.Error("compiled test binary should stay allowlisted")
	}
}
