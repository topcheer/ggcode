package security

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssue3081_C1ScannerPreservesUTF8(t *testing.T) {
	cases := []string{
		"Лa",
		"Л",
		"привет мир",
		"Δx ≥ 0",
		"emoji: 😀 ok",
		"日本語テキスト",
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

func TestIssue3081_RawC1StillStripped(t *testing.T) {

	raw := "before\x9b2Jafter"
	got := SanitizeTerminalForDisplay(raw)
	if strings.Contains(got, "\x9b") || strings.Contains(got, "2J") {
		t.Fatalf("raw C1 CSI survived: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("surrounding text damaged: %q", got)
	}

	raw2 := "x\x9dpayload\x9cy"
	got2 := SanitizeTerminalForDisplay(raw2)
	if strings.Contains(got2, "payload") {
		t.Fatalf("C1 string-sequence payload survived: %q", got2)
	}
}

func TestIssue3081_ContinuationByteNotSequenceStart(t *testing.T) {

	in := "Л2J"
	if got := SanitizeTerminalForDisplay(in); got != in {
		t.Fatalf("rune + ASCII misclassified as C1 sequence: %q -> %q", in, got)
	}
}

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

	for _, path := range []string{"handler_test.go", "fixtures/handler.test.json", "testdata/seed.yaml"} {
		if got := ScanForSecrets(path, secret); len(got) != 0 {
			t.Errorf("%s should stay allowlisted, got %d findings (V3)", path, len(got))
		}
	}
}
