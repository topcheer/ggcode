package agent

import (
	"strings"
	"testing"
)

func TestRedactSecretsMasksAKIAKey(t *testing.T) {
	in := "config:\n  key = AKIAIOSFODNN7EXAMPLE\n  region = us-east-1\n"
	out := redactSecrets("read_file", in)
	if !strings.Contains(out, "[REDACTED:aws_access_key]") {
		t.Fatalf("AWS key must be masked, got: %q", out)
	}
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatal("original secret value must not survive redaction")
	}

	if !strings.Contains(out, "region = us-east-1") {
		t.Fatal("surrounding context must be preserved")
	}
	if !strings.HasPrefix(out, "[SECURITY:") {
		t.Fatal("notice must be prepended when secrets are masked")
	}
}

func TestRedactSecretsNonExternalToolUntouched(t *testing.T) {
	in := "key = AKIAIOSFODNN7EXAMPLE"
	if out := redactSecrets("edit_file", in); out != in {
		t.Fatalf("non-external tool output must pass through unchanged, got: %q", out)
	}
}

func TestRedactSecretsNoSecretUnchanged(t *testing.T) {
	in := "just a normal file with no secrets here\n"
	if out := redactSecrets("read_file", in); out != in {
		t.Fatalf("clean content must be returned unchanged, got: %q", out)
	}
}

func TestRedactSecretsLargeContentNoTailLoss(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("prefix AKIAIOSFODNN7EXAMPLE suffix\n")
	for sb.Len() <= maxRedactScanLen {
		sb.WriteString(strings.Repeat("x", 1024) + "\n")
	}
	sb.WriteString("UNIQUE_TAIL_MARKER_9f1e")
	in := sb.String()

	out := redactSecrets("read_file", in)

	if !strings.Contains(out, "[REDACTED:aws_access_key]") {
		t.Fatal("secret in scanned window must be masked")
	}
	if !strings.Contains(out, "UNIQUE_TAIL_MARKER_9f1e") {
		t.Fatal("tail beyond scan window must be returned verbatim (no silent data loss)")
	}
	if !strings.Contains(out, "included verbatim and unscanned") {
		t.Fatal("unscanned tail must be declared in the notice")
	}

	if len(out) < len(in) {
		t.Fatalf("output must not lose content: got %d bytes, input %d", len(out), len(in))
	}
}

func TestRedactSecretsLargeCleanContentIdentical(t *testing.T) {
	var sb strings.Builder
	for sb.Len() <= maxRedactScanLen+4096 {
		sb.WriteString(strings.Repeat("y", 1024) + "\n")
	}
	in := sb.String()
	if out := redactSecrets("read_file", in); out != in {
		t.Fatalf("clean large content must be byte-identical, got len %d want %d", len(out), len(in))
	}
}

func TestCheckRedactedInWriteBlocksWriteTools(t *testing.T) {
	args := `{"path":"/etc/app.conf","old_text":"api_key: [REDACTED:assignment_secret]"}`
	for _, tool := range []string{"edit_file", "write_file", "multi_edit_file", "multi_file_edit", "notebook_edit"} {
		if w := checkRedactedInWrite(tool, args); w == "" {
			t.Fatalf("%s with REDACTED marker must produce a warning", tool)
		}
	}

	if w := checkRedactedInWrite("read_file", args); w != "" {
		t.Fatalf("read_file must not be guarded, got: %q", w)
	}
	if w := checkRedactedInWrite("edit_file", `{"path":"x","old_text":"plain"}`); w != "" {
		t.Fatalf("marker-free args must not be guarded, got: %q", w)
	}
}
