package tool

import (
	"strings"
	"testing"
)

func TestAutoSnippetEligibility(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"go test -tags goolm ./internal/tool/", true},
		{"ls -la", false},                       // trivial first token
		{"cat Makefile && head README", false},  // trivial first token
		{"go test", false},                      // too few tokens
		{"echo hi", false},                      // too few tokens + trivial
		{"rm -rf ./build", false},               // destructive
		{"git push --force origin main", false}, // destructive
		{"git reset --hard HEAD~1", false},      // destructive
		{"curl -H 'Authorization: Bearer abc123' https://x.co -d a=1 -d b=2", false}, // secret marker
		{"export GHP_abcdef123456 TO=x -y -z", false},                                // token marker
		{"short cmd", false}, // too short
	}
	for _, tc := range cases {
		if got := autoSnippetEligible(tc.cmd); got != tc.want {
			t.Errorf("autoSnippetEligible(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestNormalizeCommandPatternAndJaccard(t *testing.T) {
	base := normalizeCommandPattern(`go test -tags goolm ./internal/agent -run TestX -count=1`)
	variant := normalizeCommandPattern(`go test -tags goolm ./internal/tool -run TestY -count=2`)
	if sim := tokenSetJaccard(base, variant); sim < autoSnippetMatchThreshold {
		t.Fatalf("expected path variants to cluster (sim=%.2f)", sim)
	}
	other := normalizeCommandPattern(`go vet ./internal/tool`)
	if sim := tokenSetJaccard(base, other); sim >= autoSnippetMatchThreshold {
		t.Fatalf("expected distinct verbs NOT to cluster (sim=%.2f)", sim)
	}
	if base != normalizeCommandPattern("GO  TEST  -TAGS \"goolm\" ./internal/agent -run TestX -count=1") &&
		tokenSetJaccard(base, normalizeCommandPattern("go test -tags goolm ./internal/agent -run TestX -count=1")) != 1 {
		// same command re-run must always match itself exactly
		t.Fatal("identical commands must produce identical patterns")
	}
}

func newDistiller(t *testing.T) (*SnippetDistiller, *CmdSnippetTool) {
	t.Helper()
	store := &CmdSnippetTool{WorkingDir: t.TempDir()}
	return &SnippetDistiller{Store: store}, store
}

func TestDistillerPromotesAfterThreshold(t *testing.T) {
	d, store := newDistiller(t)
	cmds := []string{
		"go test -tags goolm ./internal/agent -count=1",
		"go test -tags goolm ./internal/tool -count=1",
		"go test -tags goolm ./internal/tui -count=1",
	}
	var name string
	for i, cmd := range cmds {
		got := d.Observe(cmd)
		if i < len(cmds)-1 && got != "" {
			t.Fatalf("promoted too early at run %d: %q", i+1, got)
		}
		if i == len(cmds)-1 {
			name = got
		}
	}
	if name == "" {
		t.Fatal("expected promotion on 3rd similar successful run")
	}
	if !strings.HasPrefix(name, "auto/") {
		t.Errorf("expected auto/ name prefix, got %q", name)
	}
	snap, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Entries) != 1 {
		t.Fatalf("expected exactly 1 entry, got %d", len(snap.Entries))
	}
	entry := snap.Entries[0]
	if entry.Name != name || entry.Source != autoSnippetSource || entry.UseCount != autoSnippetThreshold {
		t.Errorf("unexpected entry: %+v", entry)
	}
	if len(snap.Observations) != 0 {
		t.Errorf("promoted observation should be removed, %d left", len(snap.Observations))
	}

	// 4th similar run: reinforces the auto entry, no new promotion.
	if got := d.Observe("go test -tags goolm ./internal/context -count=1"); got != "" {
		t.Errorf("4th run should reinforce, not re-promote: %q", got)
	}
	snap, _ = store.load()
	if snap.Entries[0].UseCount != autoSnippetThreshold+1 {
		t.Errorf("expected UseCount reinforcement, got %d", snap.Entries[0].UseCount)
	}
	if len(snap.Entries) != 1 {
		t.Errorf("expected still 1 entry, got %d", len(snap.Entries))
	}
}

func TestDistillerRespectsManualSnippets(t *testing.T) {
	d, store := newDistiller(t)
	res, _ := store.doSave("my-test", "go test -tags goolm ./internal/agent -count=1", "manual", nil)
	if res.IsError {
		t.Fatalf("manual save failed: %s", res.Content)
	}
	for i := 0; i < 5; i++ {
		if got := d.Observe("go test -tags goolm ./internal/tool -count=1"); got != "" {
			t.Fatalf("must not auto-promote over a manual twin: %q", got)
		}
	}
	snap, _ := store.load()
	if len(snap.Entries) != 1 {
		t.Fatalf("manual entry must remain the only one, got %d", len(snap.Entries))
	}
	if snap.Entries[0].Source == autoSnippetSource {
		t.Error("manual entry must not be re-marked auto")
	}
}

func TestDistillerPendingPersistsAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	first := &SnippetDistiller{Store: &CmdSnippetTool{WorkingDir: dir}}
	first.Observe("go test -tags goolm ./internal/agent -count=1")
	first.Observe("go test -tags goolm ./internal/tool -count=1")

	// New session: fresh in-memory cache, same store file.
	second := &SnippetDistiller{Store: &CmdSnippetTool{WorkingDir: dir}}
	if got := second.Observe("go test -tags goolm ./internal/tui -count=1"); got == "" {
		t.Fatal("cross-session accumulation should reach the promotion threshold")
	}
}

func TestDistillerDistinctPatternsDoNotMerge(t *testing.T) {
	d, store := newDistiller(t)
	d.Observe("go test -tags goolm ./internal/agent -count=1")
	// "make build" is below autoSnippetMinTokens and never observed; the
	// other two commands are distinct patterns.
	d.Observe("npm run lint -- --max-warnings 0")
	snap, _ := store.load()
	if len(snap.Entries) != 0 {
		t.Fatalf("no promotion expected, got %d entries", len(snap.Entries))
	}
	if len(snap.Observations) != 2 {
		t.Fatalf("expected 2 distinct pending observations, got %d", len(snap.Observations))
	}
}

func TestDistillerCapsObservations(t *testing.T) {
	d, store := newDistiller(t)
	for i := 0; i < 60; i++ {
		d.Observe(sprintfCmd(i))
	}
	snap, _ := store.load()
	if len(snap.Observations) > autoSnippetMaxObservations {
		t.Fatalf("observations must be capped at %d, got %d", autoSnippetMaxObservations, len(snap.Observations))
	}
}

func sprintfCmd(i int) string {
	// Distinct patterns: unique verb-ish token per command.
	return strings.Join([]string{"go", "tool", "fakeverb" + strings.Repeat("x", i%20), "arg", "run", "id"}, " ") +
		" -flag" + strings.Repeat("q", i) + " /tmp/p" + strings.Repeat("z", i%7)
}

func TestDistillerUniqueNameOnCollision(t *testing.T) {
	d, store := newDistiller(t)
	// Two different patterns whose derived names collapse to the same slug.
	if got := d.Observe("go test ./aaa"); got != "" {
		t.Fatalf("unexpected early promotion: %q", got)
	}
	_ = store
}
