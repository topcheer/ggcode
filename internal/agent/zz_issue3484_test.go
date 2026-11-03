package agent

import (
	"strings"
	"testing"
)

func TestIssue3484ReadOnlyExemptionBypassClosed(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("don't touch config.yaml", "nl", refusalKindDirect)

	bypass := []string{
		`echo pwned > config.yaml`,
		`cat other.txt > config.yaml`,
		`head -n 5 config.yaml > config.yaml`,
		`git log > config.yaml`,
		`git diff --output=config.yaml`,
		`git show --output config.yaml`,
		`find . -name config.yaml -delete`,
		`find . -name "*.bak" -exec rm config.yaml \;`,
		`echo x >> config.yaml`,
		`git status 2> config.yaml`,
	}
	for _, cmd := range bypass {
		if msg := l.checkBlocked("run_command", `{"command":"`+cmd+`"}`); msg == "" {
			t.Errorf("write-evidence command must be blocked, got allow: %s", cmd)
		} else if !strings.Contains(msg, "refusal") {
			t.Errorf("block must come from refusal ledger, got: %q", msg)
		}
	}
}

func TestIssue3484ReadOnlyExemptionStillWorks(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("don't touch config.yaml", "nl", refusalKindDirect)

	readonly := []string{
		`cat config.yaml`,
		`grep 'foo' config.yaml`,
		`head -n 5 config.yaml`,
		`git log --oneline`,
		`git status`,
		`find . -name config.yaml`,
		`ls -la`,
		`echo hi`,
		`cat x <<EOF_MARKER`,
	}
	for _, cmd := range readonly {
		if msg := l.checkBlocked("run_command", `{"command":"`+cmd+`"}`); msg != "" {
			t.Errorf("read-only command must stay exempt, got block: %s (%q)", cmd, msg)
		}
	}
}

func TestIssue3484WriteEvidencePattern(t *testing.T) {
	must := []string{
		`> f`, `>> f`, `1> f`, `2> f`, `&> f`, `2>> f`,
		`-delete`, `find . -exec rm {} +`, `-execdir`, `-fprint /tmp/x`, `-fprintf`, `-fls x`,
		`--output=x`, `git log --output x`,
	}
	for _, s := range must {
		if !refusalCmdWriteEvidence.MatchString(s) {
			t.Errorf("write evidence not detected: %q", s)
		}
	}
	mustNot := []string{
		`cat f`, `head -n3 f`, `grep -r x .`, `find . -name f`,
		`cat <<EOF`, `git status`, `echo hi`, `ls -la`, `wc -l f`,
	}
	for _, s := range mustNot {
		if refusalCmdWriteEvidence.MatchString(s) {
			t.Errorf("false write evidence on read-only shape: %q", s)
		}
	}
}
