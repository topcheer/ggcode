package tool

import (
	"strings"
	"testing"
)

// Probes for the message-diff consistency sentinel (commit_consistency.go).
// Each case pins one rule of AnalyzeMessageDiffConsistency against a
// representative staged-diff shape.

const consistencyDiff = `diff --git a/internal/tool/git_commit.go b/internal/tool/git_commit.go
index 1234567..89abcde 100644
--- a/internal/tool/git_commit.go
+++ b/internal/tool/git_commit.go
@@ -120,6 +120,9 @@
+func NewSymbol() error {
+func (t GitCommit) Clone() Tool {
-const oldLimit = 3
diff --git a/internal/tool/commit_analyzer.go b/internal/tool/commit_analyzer.go
index 1111111..2222222 100644
--- a/internal/tool/commit_analyzer.go
+++ b/internal/tool/commit_analyzer.go
@@ -1,2 +1,3 @@
+var AnalyzeCommitScope = scopeAnalyzer
`

func TestAnalyzeMessageDiffConsistency_Agreed(t *testing.T) {
	msg := "fix(tool): NewSymbol conflicts with oldLimit in git_commit.go"
	got := AnalyzeMessageDiffConsistency(msg, consistencyDiff)
	if got != "" {
		t.Fatalf("consistent message must produce no warning, got: %s", got)
	}
}

func TestAnalyzeMessageDiffConsistency_PhantomFile(t *testing.T) {
	msg := "fix(tool): repair checkCommitMessageQuality in provider_panel.go"
	got := AnalyzeMessageDiffConsistency(msg, consistencyDiff)
	if got == "" {
		t.Fatal("message naming a file absent from the diff must warn")
	}
	if !strings.Contains(got, "provider_panel.go") {
		t.Fatalf("warning must name the phantom file, got: %s", got)
	}
}

// TestAnalyzeMessageDiffConsistency_AbbreviationNoFalsePositive (#3448):
// the highest-frequency English abbreviations all match msgFileTokenRe and
// used to produce constant phantom-file warnings (~1.2% of this repo's
// recent commits contain "e.g."). They must be filtered as
// abbreviation-shaped tokens, while real phantom detection stays live.
func TestAnalyzeMessageDiffConsistency_AbbreviationNoFalsePositive(t *testing.T) {
	for _, msg := range []string{
		"fix(agent): retry on transient errors, e.g. 429/503",
		"refactor config parsing, i.e. the yaml loader",
		"symbols like U.S and a.m must not be files",
		"see x.y for details", // also covers a.k.a shape
	} {
		if got := AnalyzeMessageDiffConsistency(msg, consistencyDiff); got != "" {
			t.Fatalf("abbreviation-only message must not warn, msg=%q got: %s", msg, got)
		}
	}
	// Real phantom detection still fires alongside an abbreviation.
	got := AnalyzeMessageDiffConsistency(
		"fix(agent): retry on transient errors, e.g. 429/503 (see provider_panel.go)", consistencyDiff)
	if !strings.Contains(got, "provider_panel.go") {
		t.Fatalf("real phantom must still warn, got: %s", got)
	}
}

func TestAnalyzeMessageDiffConsistency_PhantomSymbols(t *testing.T) {
	msg := "refactor(tool): extract ValidateHarnessState and ResetProbeBuffer helpers"
	got := AnalyzeMessageDiffConsistency(msg, consistencyDiff)
	if got == "" {
		t.Fatal("two symbols absent from the diff must warn")
	}
	if !strings.Contains(got, "ValidateHarnessState") {
		t.Fatalf("warning must name a phantom symbol, got: %s", got)
	}
}

func TestAnalyzeMessageDiffConsistency_SingleSymbolNoWarn(t *testing.T) {
	// One borderline identifier is frequently legitimate prose — precision
	// guard: require two before warning.
	msg := "chore(tool): minor cleanup around ParseJSONShape"
	got := AnalyzeMessageDiffConsistency(msg, consistencyDiff)
	if got != "" {
		t.Fatalf("single unseen symbol must not warn, got: %s", got)
	}
}

func TestAnalyzeMessageDiffConsistency_Coverage(t *testing.T) {
	multi := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n" +
		"diff --git a/b/c.go b/b/c.go\n--- a/b/c.go\n+++ b/b/c.go\n" +
		"diff --git a/d.go b/d.go\n--- a/d.go\n+++ a/d.go\n" +
		"diff --git a/e/f.go b/e/f.go\n--- a/e/f.go\n+++ a/e/f.go\n" +
		"diff --git a/g.go b/g.go\n--- a/g.go\n+++ a/g.go\n"
	got := AnalyzeMessageDiffConsistency("release: minor fixes", multi)
	if got == "" {
		t.Fatal("5-file diff with a message naming none of them must warn")
	}
	if !strings.Contains(got, "5 files") {
		t.Fatalf("coverage warning must state the file count, got: %s", got)
	}
}

func TestAnalyzeMessageDiffConsistency_EmptyInputs(t *testing.T) {
	if got := AnalyzeMessageDiffConsistency("", consistencyDiff); got != "" {
		t.Fatalf("empty message must not warn, got: %s", got)
	}
	if got := AnalyzeMessageDiffConsistency("fix: something", ""); got != "" {
		t.Fatalf("empty diff must not warn, got: %s", got)
	}
	if got := AnalyzeMessageDiffConsistency("   ", "  "); got != "" {
		t.Fatalf("blank inputs must not warn, got: %s", got)
	}
}

func TestLooksLikeProse(t *testing.T) {
	prose := []string{"The", "Warning", "about", "TODO", "Message"}
	for _, w := range prose {
		if !looksLikeProse(w) {
			t.Errorf("%q should classify as prose", w)
		}
	}
	idents := []string{"NewHandler", "parseJSON", "context_manager", "MAX_RETRIES", "ParseJSONShape"}
	for _, w := range idents {
		if looksLikeProse(w) {
			t.Errorf("%q should classify as identifier-shaped", w)
		}
	}
}

func TestPathBase(t *testing.T) {
	if pathBase("a/b/c.go") != "c.go" {
		t.Errorf("pathBase mismatch: %s", pathBase("a/b/c.go"))
	}
	if pathBase("plain.go") != "plain.go" {
		t.Errorf("pathBase mismatch: %s", pathBase("plain.go"))
	}
}
