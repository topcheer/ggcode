package security

import (
	"strings"
	"testing"
)

// #3084 V2: detection layer must flag the ENCRYPTED (passphrase-protected)
// private key form, matching the display layer's coverage.
func TestIssue3084EncryptedPEMDetected(t *testing.T) {
	const body = "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKYwgg\n-----END ENCRYPTED PRIVATE KEY-----"
	findings := ScanForSecrets("config/prod.key", body)
	found := false
	for _, f := range findings {
		if f.PatternID == "private_key_block" {
			found = true
		}
	}
	if !found {
		t.Errorf("ENCRYPTED PRIVATE KEY not flagged by private_key_block; findings=%v", findings)
	}
	// plain forms still detected (no regression)
	if ScanForSecrets("config/k.key", "-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY-----") == nil {
		t.Error("RSA PRIVATE KEY regression")
	}
}

// #3084 V1: prose that merely MENTIONS the literal header must not be
// masked to end-of-buffer; a genuine truncated key body still is.
func TestIssue3084TruncatedPEMGate(t *testing.T) {
	// prose: header followed by ordinary explanation text (no base64 run)
	prose := "-----BEGIN PRIVATE KEY-----\nThe PEM format wraps a base64 body between header and footer lines, as documented here."
	if out := RedactForDisplay(prose); out != prose {
		t.Errorf("prose mention masked: %q", out)
	}
	// genuine truncated key: header + 20+ base64 chars, no END
	truncated := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC"
	out := RedactForDisplay(truncated)
	if strings.Contains(out, "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC") {
		t.Errorf("truncated key body leaked: %q", out)
	}
	// complete block still masked by the original pattern
	complete := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----"
	if out2 := RedactForDisplay(complete); strings.Contains(out2, "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC") {
		t.Errorf("complete block body leaked: %q", out2)
	}
}
