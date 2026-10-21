package agent

// zz_issue4338_test.go -- regression nail for #4338: the #3484 write-evidence
// list only covered redirection and find write-flags, so mid-command write
// vectors past a read-only prefix (pipe consumers, xargs writers, compound
// separators chaining into rm/chmod/...) fail-opened the read-only exemption
// and silently pierced the 30-day persisted-refusal block.

import (
	"strings"
	"testing"
)

// TestIssue4338PipeAndCompoundWriteVectorsBlocked: every bypass shape from
// the issue reproduction table must be BLOCKED when it targets the refused
// file (the exemption must be disqualified and normal entry matching run).
func TestIssue4338PipeAndCompoundWriteVectorsBlocked(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("never touch config.yaml", "nl", refusalKindDirect)

	bypass := []string{
		`cat README.md | tee config.yaml`,       // pipe consumer overwrites
		`grep foo bar.txt | tee -a config.yaml`, // append via pipe
		`find . -name config.yaml | xargs rm`,   // xargs destructive feed
		`ls | xargs chmod -x config.yaml`,       // xargs metadata write
		`cat config.yaml | xargs rm`,            // xargs delete
		`pwd && rm config.yaml`,                 // compound && into rm
		`echo hi; rm -rf config.yaml`,           // compound ; into rm
		`git log --oneline || rm config.yaml`,   // compound || into rm
		`cat a.md | sponge config.yaml`,         // sponge overwrite
		`cat a.md | dd of=config.yaml`,          // dd of= write
		`cat a.md | truncate config.yaml`,       // truncate
	}
	for _, cmd := range bypass {
		if msg := l.checkBlocked("run_command", `{"command":"`+cmd+`"}`); msg == "" {
			t.Errorf("pipe/compound write vector must be blocked, got allow: %s", cmd)
		} else if !strings.Contains(msg, "refusal") {
			t.Errorf("block must come from refusal ledger, got: %q", msg)
		}
	}
}

// TestIssue4338ReadOnlyShapesStillExempt: the widened evidence must not
// swallow genuinely read-only shapes - pipes into readers and compound
// chains of readers stay exempt (#3469 note preserved).
func TestIssue4338ReadOnlyShapesStillExempt(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("never touch config.yaml", "nl", refusalKindDirect)

	readonly := []string{
		`cat config.yaml | grep foo`,       // pipe into reader
		`grep -c foo config.yaml | wc -l`,  // reader chain
		`cat a.md && cat config.yaml`,      // compound of readers
		`echo hi; ls`,                      // compound benign
		`find . -name config.yaml | wc -l`, // find pipe into wc
		`which cat`,                        // plain read-only
		`stat config.yaml`,                 // inspect refused target read-only
		`cat x <<EOF_MARKER`,               // heredoc input-side (3484 control)
	}
	for _, cmd := range readonly {
		if msg := l.checkBlocked("run_command", `{"command":"`+cmd+`"}`); msg != "" {
			t.Errorf("read-only command must stay exempt, got block: %s (%q)", cmd, msg)
		}
	}
}

// TestIssue4338WriteEvidencePattern: unit-nail the widened pattern so a
// future regex tweak cannot silently reopen a bypass shape.
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
