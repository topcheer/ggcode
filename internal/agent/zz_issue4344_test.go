package agent

// zz_issue4344_test.go -- regression nail for #4344: #4338's write-evidence
// list was writer+separator ENUMERATION, so combination shapes past a
// read-only prefix still fail-opened the exemption: newline separators,
// pipes into interpreters/ssh, and xargs feeding path-qualified or
// env-wrapped writers.

import (
	"strings"
	"testing"
)

func TestIssue4344ResidualWriteVectorsPattern(t *testing.T) {
	must := []string{
		"echo hi\nrm -rf config.yaml", // newline separator
		"cat f | sh",                  // pipe into interpreter
		"cat f | bash",
		"cat f | python -c 'open(\"x\",\"w\")'",
		"cat f | python3 scrub.py",
		"cat list.txt | perl -ne 'unlink'",
		"find . -name '*.log' | xargs /bin/rm", // path-qualified writer
		"ls | xargs env rm",                    // env-wrapped writer
		"find . | xargs sudo rm",               // sudo-wrapped writer (#4344 delta re-review)
		"find . | xargs sh -c 'rm -f $0'",      // xargs shell
		"cat f | ssh host 'rm -f x'",           // remote sink, no redirect
		"echo done; sh cleanup.sh",             // ';' into shell
	}
	for _, s := range must {
		if !refusalCmdWriteEvidence.MatchString(s) {
			t.Errorf("write evidence not detected: %q", s)
		}
	}
	// Reader chains and benign shapes must stay exempt-eligible.
	mustNot := []string{
		"echo hi; ls",
		"cat a | grep x",
		"cat a | wc -l",
		"cat a | sort",
		"cat f | sha256sum", // 'sh' inside sha256sum: no \b
		"cat f | shasum",
		"grep -c foo bar | wc -l",
		"cat a.md && cat config.yaml",
		"find . -name config.yaml | wc -l",
		"which cat",
		"cat x <<EOF_MARKER",
	}
	for _, s := range mustNot {
		if refusalCmdWriteEvidence.MatchString(s) {
			t.Errorf("read-only shape falsely flagged as write evidence: %q", s)
		}
	}
}

func TestIssue4344ResidualVectorsBlocked(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("never touch config.yaml", "nl", refusalKindDirect)

	bypass := []string{
		"echo hi\\nrm -rf config.yaml", // JSON-escaped newline -> decoded separator
		"cat config.yaml | bash",
		"find . -name config.yaml | xargs /bin/rm",
		"ls | xargs env rm config.yaml",
		"find . -name config.yaml | xargs sh -c 'rm -f $0'",
		"cat config.yaml | ssh host 'cat > /tmp/x'",
	}
	for _, cmd := range bypass {
		if msg := l.checkBlocked("run_command", `{"command":"`+cmd+`"}`); msg == "" {
			t.Errorf("residual write vector must be blocked, got allow: %s", cmd)
		} else if !strings.Contains(msg, "refusal") {
			t.Errorf("block must come from refusal ledger, got: %q", msg)
		}
	}

	readonly := []string{
		"echo hi; ls",
		"cat config.yaml | grep foo",
		"stat config.yaml",
	}
	for _, cmd := range readonly {
		if msg := l.checkBlocked("run_command", `{"command":"`+cmd+`"}`); msg != "" {
			t.Errorf("read-only command must stay exempt, got block: %s (%q)", cmd, msg)
		}
	}
}
