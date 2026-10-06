package security

// Regression probes for #3081 (internal/security, 3-point package):
//   V1: byte-level C1 regexes (\x9b CSI, 0x90/0x98/0x9d/0x9e/0x9f string
//       intros) matched INSIDE multi-byte UTF-8 runes - Cyrillic "Лa"
//       (D0 9B 61) lost 9B+61 to the CSI pattern and emitted invalid UTF-8.
//   V2: the display layer masked only COMPLETE PEM blocks; a truncated
//       stream's bare "-----BEGIN PRIVATE KEY-----" header (plus any
//       partial body) rendered raw while the detection layer alarms on
//       BEGIN alone.
//   V3: the bare `\.test\.` allowlist infix skipped real configs
//       (api.test.env) from secret scanning.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// V1: Cyrillic + ASCII must survive sanitization byte-for-byte.
func TestIssue3081_C1ScannerPreservesUTF8(t *testing.T) {
	cases := []string{
		"Лa",          // D0 9B 61: the issue's exact corruption case
		"Л",           // lone Cyrillic El (D0 9B)
		"привет мир",  // running Cyrillic text
		"Δx ≥ 0",      // Greek + math
		"emoji: 😀 ok", // four-byte rune F0 9F 98 80
		"日本語テキスト",     // CJK
	}
	for _, in := range cases {
		got := SanitizeTerminalForDisplay(in)
		if got != in {
			t.Errorf("SanitizeTerminalForDisplay(%q) = %q, want unchanged (V1)", in, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("output invalid UTF-8 for input %q: %q", in, got)
		}
	}
}

// V1 companion: GENUINE raw C1 sequences (standalone invalid bytes) must
// still be stripped - the fix must not become a pass-through.
func TestIssue3081_RawC1StillStripped(t *testing.T) {
	// Raw CSI 0x9b "2J" (clear screen) as standalone bytes.
	raw := "before\x9b2Jafter"
	got := SanitizeTerminalForDisplay(raw)
	if strings.Contains(got, "\x9b") || strings.Contains(got, "2J") {
		t.Fatalf("raw C1 CSI survived: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("surrounding text damaged: %q", got)
	}
	// Raw OSC-form C1 string sequence 0x9d ... 0x9c.
	raw2 := "x\x9dpayload\x9cy"
	got2 := SanitizeTerminalForDisplay(raw2)
	if strings.Contains(got2, "payload") {
		t.Fatalf("C1 string-sequence payload survived: %q", got2)
	}
}

// V1: a raw C1 CSI that begins with a continuation byte INSIDE a rune must
// not be treated as a sequence start - "Л" followed by params.
func TestIssue3081_ContinuationByteNotSequenceStart(t *testing.T) {
	// Л (D0 9B) + "2J": the 9B is part of Л, and "2J" is plain text.
	in := "Л2J"
	if got := SanitizeTerminalForDisplay(in); got != in {
		t.Fatalf("rune + ASCII misclassified as C1 sequence: %q -> %q", in, got)
	}
}

// V2: bare PEM header without END must be masked on the display layer.
func TestIssue3081_BarePemHeaderMasked(t *testing.T) {
	in := "log line\n-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCB\n(truncated"
	got := RedactForDisplay(in)
	if strings.Contains(got, "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCB") {
		t.Fatalf("truncated key body leaked past bare header (V2): %q", got)
	}
	if strings.Contains(got, "-----BEGIN PRIVATE KEY-----") {
		t.Fatalf("bare PEM header not masked (V2): %q", got)
	}
}

// V2 companion: complete blocks still use the exact-block pattern (parity
// with the #1626-B pin) and normal text is untouched.
func TestIssue3081_CompleteBlockStillMasked(t *testing.T) {
	in := "-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY-----"
	got := RedactForDisplay(in)
	if strings.Contains(got, "MIIB") {
		t.Fatalf("complete block body leaked: %q", got)
	}
	if RedactForDisplay("plain text, nothing secret") != "plain text, nothing secret" {
		t.Fatal("plain text mutated")
	}
}

func TestIssue3081_TestInfixConfigsScanned(t *testing.T) {
	secret := "api_key = \"sk-ant-abcdefghijklmnopqrstuvwxyz0123456789012345678901234567890123456\""
	for _, path := range []string{"api.test.env", "prod.test.conf", "config/test.yaml"} {
		if got := ScanForSecrets(path, secret); len(got) == 0 {
			t.Errorf("%s skipped by allowlist but must be scanned (V3)", path)
		}
	}
	// Genuine test resources stay allowlisted.
	for _, path := range []string{"handler_test.go", "fixtures/handler.test.json", "testdata/seed.yaml"} {
		if got := ScanForSecrets(path, secret); len(got) != 0 {
			t.Errorf("%s should stay allowlisted, got %d findings (V3)", path, len(got))
		}
	}
}
