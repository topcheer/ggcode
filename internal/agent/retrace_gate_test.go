package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestRetraceShouldCheck(t *testing.T) {
	base := postEditVerifyState{sourceEditsThisRun: 2, realBuildOrTestRunThisRun: true}
	cases := []struct {
		name     string
		pv       postEditVerifyState
		research bool
		fired    bool
		want     bool
	}{
		{"happy path", base, false, false, true},
		{"no edits", postEditVerifyState{sourceEditsThisRun: 0, realBuildOrTestRunThisRun: true}, false, false, false},
		{"no receipt", postEditVerifyState{sourceEditsThisRun: 3, realBuildOrTestRunThisRun: false}, false, false, false},
		{"research mode", base, true, false, false},
		{"already fired", base, false, true, false},
	}
	for _, c := range cases {
		if got := retraceShouldCheck(c.pv, c.research, c.fired); got != c.want {
			t.Errorf("%s: retraceShouldCheck = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBackwardIsolation(t *testing.T) {
	task := "Fix the login timeout bug in session.go and add a regression test"
	diff := "diff --git a/internal/session.go b/internal/session.go\n+timeout = 30"
	msgs := buildBackwardMessages(diff)
	for i, m := range msgs {
		blob := ""
		for _, c := range m.Content {
			if c.Text != "" {
				blob += c.Text
			}
		}
		if strings.Contains(blob, task) {
			t.Fatalf("backward message %d leaks the task text (anchoring broken)", i)
		}
		if !strings.Contains(blob, diff) {
			t.Fatalf("backward message %d missing the diff", i)
		}
	}

	rec := buildReconcileMessages(task, "reconstruction", diff)
	found := false
	for _, m := range rec {
		for _, c := range m.Content {
			if strings.Contains(c.Text, task) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("reconcile message must include the task text")
	}
}

func TestParseRetraceVerdict(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"aligned", "analysis...\nRETRACE: aligned", "aligned"},
		{"misaligned", "RETRACE: misaligned", "misaligned"},
		{"partial with annotation", "RETRACE: partial (test half done)", "partial"},
		{"no marker", "looks fine to me", ""},
		{"garbage after marker", "RETRACE: maybe", ""},
		{"marker scrolled out", "RETRACE: aligned\nl1\nl2\nl3\nl4\nl5\nl6\nl7", ""},
	}
	for _, c := range cases {
		v, guidance := parseRetraceVerdict(c.out)
		if v != c.want {
			t.Errorf("%s: verdict = %q, want %q", c.name, v, c.want)
		}
		if v != "" && guidance == "" {
			t.Errorf("%s: guidance empty despite verdict", c.name)
		}
	}
}

func TestRetraceInjectMessage(t *testing.T) {
	for _, v := range []string{"partial", "misaligned"} {
		msg := retraceInjectMessage(v, "PROBLEM: X\nSCOPE: half\nRevision: add the missing test")
		if !strings.Contains(msg, "[retrace-gate]") {
			t.Errorf("%s: injection missing gate tag", v)
		}
		if !strings.Contains(msg, "add the missing test") {
			t.Errorf("%s: injection missing reviewer guidance", v)
		}
	}

	long := strings.Repeat("x", 5000)
	msg := retraceInjectMessage("misaligned", long)
	if len(msg) > retraceMsgCap+100 {
		t.Fatalf("injection not capped: %d bytes", len(msg))
	}
}

func TestRetraceGateStateZeroValue(t *testing.T) {
	var s retraceGateState
	if s.firedThisRun {
		t.Fatal("zero-value retraceGateState must not be fired")
	}
	_ = context.Background()
}

func TestRetraceVerificationSurvivesProviderPanic(t *testing.T) {
	a := &Agent{provider: &panicStreamProvider{}}
	done := make(chan string, 1)
	go func() {
		done <- a.runRetraceVerification(context.Background(), "fix the bug", "diff --git a/x.go b/x.go")
	}()
	select {
	case msg := <-done:
		if msg != "" {
			t.Fatalf("panic must pass through with empty injection, got %.60q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gate did not recover the panic fast enough — likely took the stall-timeout path, not the recover path")
	}
}

type panicStreamProvider struct{ provider.Provider }

func (p *panicStreamProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	var nilMap map[string]int
	nilMap["boom"] = 1
	return nil, nil
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-q", "-m", "base")
	return dir
}

func TestUncommittedDiffUntrackedOnly(t *testing.T) {
	dir := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "newmod.go"), []byte("package newmod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := uncommittedDiff(dir)
	if !strings.Contains(got, "newmod.go") || !strings.Contains(got, "package newmod") {
		t.Fatalf("scenario A: untracked-only delivery invisible to retrace gate, diff =\n%s", got)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("scenario A: empty diff for untracked-only delivery")
	}
}

func TestUncommittedDiffMixed(t *testing.T) {
	dir := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package helper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := uncommittedDiff(dir)
	if !strings.Contains(got, "edited") {
		t.Fatalf("scenario B: tracked modification missing from diff:\n%s", got)
	}
	if !strings.Contains(got, "helper.go") || !strings.Contains(got, "package helper") {
		t.Fatalf("scenario B: untracked half missing from diff (biased reconstruction):\n%s", got)
	}
}

func TestUncommittedDiffIgnoresIgnored(t *testing.T) {
	dir := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "noise.log"), []byte("build noise\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := uncommittedDiff(dir); strings.Contains(got, "noise.log") {
		t.Fatalf("ignored file leaked into retrace diff:\n%s", got)
	}
}
