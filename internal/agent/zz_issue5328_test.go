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
//
// Known edge cases deliberately OUT OF SCOPE here (documented per #5329
// review rather than silently missed):
//   - `cat <<EOF | bash` -- a TEXT receiver consumes the body but pipes it
//     to an interpreter; the body is stripped as text and the piped bash
//     execution is not tracked. Same family: `cat file | bash` without any
//     heredoc. Detecting pipeline consumers requires command-graph parsing,
//     which this guard does not attempt.
//   - Multi-stage indirection (body writes a script, later command runs it)
//     is inherently invisible to a single-command lexical scan.
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

func TestIssue5329ReceiverClassificationFailSafe(t *testing.T) {
	// Control group from the #5329 NO-GO review: path forms, wrapper
	// prefixes, container execs, and unknown receivers must all KEEP the
	// body (default = fail-safe keep). Previously the classifier matched an
	// interpreter whitelist and stripped everything else -- inverting the
	// required default.
	fireCases := []struct{ name, start string }{
		{"path form /bin/bash", "/bin/bash <<'EOF'"},
		{"path form ./tool", "./mytool <<'EOF'"},
		{"sudo bash", "sudo bash <<'EOF'"},
		{"env bash", "env bash <<EOF"},
		{"time sh", "time sh <<EOF"},
		{"docker exec sh", "docker exec -i mycontainer sh <<'EOF'"},
		{"unknown command", "mytool <<EOF"},
		{"VAR=val prefixed", "FOO=1 bash <<EOF"},
	}
	for _, tc := range fireCases {
		cmd := tc.start + "\n" + rmRfLine(t) + " $HOME/data\nEOF"
		if !strings.Contains(stripHeredocBodies(cmd), "rm -rf") {
			t.Errorf("%s: body must be KEPT in scanned text (fail-safe default), got stripped", tc.name)
		}
	}
	// sudo -u flag form: classifier yields "-u" (not a text receiver), so
	// the body stays visible -- fail-safe direction, pinned explicitly.
	flagged := "sudo -u deploy bash <<'EOF'\n" + rmRfLine(t) + " /srv\nEOF"
	if !strings.Contains(stripHeredocBodies(flagged), "rm -rf") {
		t.Error("sudo -u ... bash: flag form must keep body (receiver resolution falls to non-text token)")
	}
}

func rmRfLine(t *testing.T) string {
	t.Helper()
	if os.Getenv("GGCODE_TEST_LONG_LINE") == "1" {
		return strings.Repeat("x", 10) + " " + "rm" + " -Rf"
	}
	return "rm" + " -rf"
}
