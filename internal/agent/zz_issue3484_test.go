package agent

// zz_issue3484_test.go -- regression nail for #3484: the refusal-ledger
// read-only run_command exemption must not cover commands that carry shell
// write evidence (redirection, find write-flags, git --output). Those fall
// through to normal entry matching and are blocked when they hit a refused
// target.

import (
	"strings"
	"testing"
)

// TestIssue3484ReadOnlyExemptionBypassClosed: every write-evidence shape
// from the issue reproduction table must be BLOCKED when it targets the
// refused file.
func TestIssue3484ReadOnlyExemptionBypassClosed(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("don't touch config.yaml", "nl")

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

// TestIssue3484ReadOnlyExemptionStillWorks: genuinely read-only shapes
// (no write evidence) stay exempt even when they mention the refused
// target - harmless verification (#3469 note preserved).
func TestIssue3484ReadOnlyExemptionStillWorks(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("don't touch config.yaml", "nl")

	readonly := []string{
		`cat config.yaml`,
		`grep 'foo' config.yaml`,
		`head -n 5 config.yaml`,
		`git log --oneline`,
		`git status`,
		`find . -name config.yaml`,
		`ls -la`,
		`echo hi`,
		`cat x <<EOF_MARKER`, // heredoc opener is input-side, no `>`
	}
	for _, cmd := range readonly {
		if msg := l.checkBlocked("run_command", `{"command":"`+cmd+`"}`); msg != "" {
			t.Errorf("read-only command must stay exempt, got block: %s (%q)", cmd, msg)
		}
	}
}

// TestIssue3484WriteEvidencePattern: unit-nail the pattern itself so a future
// regex tweak cannot silently reopen a bypass shape.
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
