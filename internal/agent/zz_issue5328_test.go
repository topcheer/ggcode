package agent

import (
	"os"
	"strings"
	"testing"
)

// Pins #5328: interpreter heredocs (bash/sh/python3 <<EOF) hold EXECUTED
// code, so their bodies must stay visible to the rm scan. #5315 stripped
// every heredoc body regardless of receiver, making the rm_rf critical
// guard silent for recursive force-deletes inside interpreter heredocs,
// while text receivers (cat/tee/wc, plain redirection) keep the intended
// FP fix.
func TestIssue5328InterpreterHeredocBodiesVisible(t *testing.T) {
	// bash/sh: the rm line IS a shell command position inside the body -
	// after the fix it must be seen by firstRmCommandSegment (old #5315
	// code went blind here).
	shellCases := []struct{ name, cmd string }{
		{"bash", "bash <<'EOF'\n" + rmRfLine(t) + " $HOME/important\nEOF"},
		{"sh", "sh <<EOF\n" + rmRfLine(t) + " /tmp/build\nEOF"},
	}
	for _, tc := range shellCases {
		if seg := firstRmCommandSegment(tc.cmd); seg == "" {
			t.Errorf("%s: rm inside interpreter heredoc body must stay visible to firstRmCommandSegment (guard must not go blind)", tc.name)
		}
	}
	// python3: a bare rm mention inside the body is a string literal, not a
	// shell command position - firstRmCommandSegment correctly does not
	// fire. The #5328 contract for interpreter receivers is that the BODY
	// STAYS VISIBLE to the raw scan (no unconditional strip), so downstream
	// pattern layers can see it.
	py := "python3 <<EOF\nprint('" + rmRfLine(t) + " /tmp')\nEOF"
	if !strings.Contains(stripHeredocBodies(py), "rm -rf") {
		t.Error("python3 interpreter heredoc body must NOT be stripped (executed code stays visible to raw scan)")
	}
}

func TestIssue5328TextReceiverHeredocStillStripped(t *testing.T) {
	rm := "rm" + " -rf"
	textCases := []string{
		"cat >> ledger.md <<'EOF'\n" + rm + " ~/important\nEOF",
		"cat <<EOF > note.txt\nthe text " + rm + " tmp appears here\nEOF",
		">> log.md <<'EOF'\ndocumented " + rm + " behaviour\nEOF",
	}
	for _, cmd := range textCases {
		if seg := firstRmCommandSegment(cmd); seg != "" {
			t.Errorf("text-receiver heredoc must keep the #5315 FP fix: body must be stripped (got segment %q)", seg)
		}
	}
}

func rmRfLine(t *testing.T) string {
	t.Helper()
	if os.Getenv("GGCODE_TEST_LONG_LINE") == "1" {
		return strings.Repeat("x", 10) + " " + "rm" + " -Rf"
	}
	return "rm" + " -rf"
}
