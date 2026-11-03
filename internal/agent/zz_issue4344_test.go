package agent

import (
	"strings"
	"testing"
)

func TestIssue4344ResidualWriteVectorsPattern(t *testing.T) {
	must := []string{
		"echo hi\nrm -rf config.yaml",
		"cat f | sh",
		"cat f | bash",
		"cat f | python -c 'open(\"x\",\"w\")'",
		"cat f | python3 scrub.py",
		"cat list.txt | perl -ne 'unlink'",
		"find . -name '*.log' | xargs /bin/rm",
		"ls | xargs env rm",
		"find . | xargs sudo rm",
		"find . | xargs sh -c 'rm -f $0'",
		"cat f | ssh host 'rm -f x'",
		"echo done; sh cleanup.sh",
	}
	for _, s := range must {
		if !refusalCmdWriteEvidence.MatchString(s) {
			t.Errorf("write evidence not detected: %q", s)
		}
	}

	mustNot := []string{
		"echo hi; ls",
		"cat a | grep x",
		"cat a | wc -l",
		"cat a | sort",
		"cat f | sha256sum",
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
		"echo hi\\nrm -rf config.yaml",
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
