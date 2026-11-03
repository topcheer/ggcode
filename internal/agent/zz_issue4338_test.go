package agent

import (
	"strings"
	"testing"
)

func TestIssue4338PipeAndCompoundWriteVectorsBlocked(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("never touch config.yaml", "nl", refusalKindDirect)

	bypass := []string{
		`cat README.md | tee config.yaml`,
		`grep foo bar.txt | tee -a config.yaml`,
		`find . -name config.yaml | xargs rm`,
		`ls | xargs chmod -x config.yaml`,
		`cat config.yaml | xargs rm`,
		`pwd && rm config.yaml`,
		`echo hi; rm -rf config.yaml`,
		`git log --oneline || rm config.yaml`,
		`cat a.md | sponge config.yaml`,
		`cat a.md | dd of=config.yaml`,
		`cat a.md | truncate config.yaml`,
	}
	for _, cmd := range bypass {
		if msg := l.checkBlocked("run_command", `{"command":"`+cmd+`"}`); msg == "" {
			t.Errorf("pipe/compound write vector must be blocked, got allow: %s", cmd)
		} else if !strings.Contains(msg, "refusal") {
			t.Errorf("block must come from refusal ledger, got: %q", msg)
		}
	}
}

func TestIssue4338ReadOnlyShapesStillExempt(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("never touch config.yaml", "nl", refusalKindDirect)

	readonly := []string{
		`cat config.yaml | grep foo`,
		`grep -c foo config.yaml | wc -l`,
		`cat a.md && cat config.yaml`,
		`echo hi; ls`,
		`find . -name config.yaml | wc -l`,
		`which cat`,
		`stat config.yaml`,
		`cat x <<EOF_MARKER`,
	}
	for _, cmd := range readonly {
		if msg := l.checkBlocked("run_command", `{"command":"`+cmd+`"}`); msg != "" {
			t.Errorf("read-only command must stay exempt, got block: %s (%q)", cmd, msg)
		}
	}
}

func TestIssue4338WriteEvidencePattern(t *testing.T) {
	must := []string{
		`cat a | tee f`, `cat a | tee -a f`, `| sponge f`, `| truncate f`,
		`| dd of=f`, `find . | xargs rm`, `ls | xargs chmod -x`,
		`pwd && rm f`, `echo hi; rm -rf b/`, `git log || rm f`,
	}
	for _, s := range must {
		if !refusalCmdWriteEvidence.MatchString(s) {
			t.Errorf("write evidence not detected: %q", s)
		}
	}
	mustNot := []string{
		`cat a | grep x`, `cat a | wc -l`, `grep x f && cat g`,
		`echo hi; ls`, `find . -name f | wc -l`, `which cat`,
	}
	for _, s := range mustNot {
		if refusalCmdWriteEvidence.MatchString(s) {
			t.Errorf("read-only shape falsely flagged as write evidence: %q", s)
		}
	}
}
