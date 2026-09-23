package vcs

// sa-107: deterministic, binary-free coverage net for the hg/jj/svn backends
// plus git.Checkout. The real-binary integration suite
// (vcs_integration_test.go, //go:build integration_local) exercises these
// backends against actual hg/jj/svn installations, but that build tag excludes
// it from default `go test` runs — leaving hg.go/jj.go/svn.go at ~0% coverage
// in CI and pre-commit verification. Here a PATH-injected fake recorder
// binary asserts argument construction, multi-call sequences, fallback
// ordering and failure short-circuits without requiring any real VCS binary.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBinScript is installed under the requested command name. Each
// invocation appends "$*" to $FAKEVCS_ARGS, bumps the counter file
// $FAKEVCS_COUNT, fails when the counter is <= $FAKEVCS_FAIL_UNTIL or
// > $FAKEVCS_FAIL_AFTER, and prints $FAKEVCS_OUT (or $FAKEVCS_OUT2 from the
// second invocation on) to stdout.
const fakeBinScript = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKEVCS_ARGS"
n=$(cat "$FAKEVCS_COUNT" 2>/dev/null || echo 0)
n=$((n+1))
echo "$n" > "$FAKEVCS_COUNT"
if [ -n "$FAKEVCS_FAIL_UNTIL" ] && [ "$n" -le "$FAKEVCS_FAIL_UNTIL" ]; then
	echo "fake failure $n" >&2
	exit 1
fi
if [ -n "$FAKEVCS_FAIL_AFTER" ] && [ "$n" -gt "$FAKEVCS_FAIL_AFTER" ]; then
	echo "fake failure after $n" >&2
	exit 1
fi
if [ -n "$FAKEVCS_FAIL" ]; then
	echo "fake failure $n" >&2
	exit 1
fi
out="$FAKEVCS_OUT"
if [ "$n" -gt 1 ] && [ -n "$FAKEVCS_OUT2" ]; then
	out="$FAKEVCS_OUT2"
fi
if [ -n "$out" ]; then
	printf '%s\n' "$out"
fi
`

// setupFakeBin shadows the given VCS command name with the fake recorder for
// the duration of the test (t.Setenv auto-restores PATH and the FAKEVCS_*
// variables) and returns a scratch working directory plus the path of the
// recorded-invocations log.
func setupFakeBin(t *testing.T, name string) (workDir string, logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake VCS binaries require a POSIX shell")
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, name), []byte(fakeBinScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	fakeEnv := map[string]string{
		"FAKEVCS_ARGS":       filepath.Join(binDir, "calls.log"),
		"FAKEVCS_COUNT":      filepath.Join(binDir, "calls.count"),
		"FAKEVCS_OUT":        "",
		"FAKEVCS_OUT2":       "",
		"FAKEVCS_FAIL":       "",
		"FAKEVCS_FAIL_UNTIL": "",
		"FAKEVCS_FAIL_AFTER": "",
	}
	for k, v := range fakeEnv {
		t.Setenv(k, v)
	}
	return t.TempDir(), filepath.Join(binDir, "calls.log")
}

// recordedCalls returns one string per recorded invocation, e.g.
// "commit -m msg1". Fails the test when nothing was recorded.
func recordedCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake call log: %v", err)
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			calls = append(calls, line)
		}
	}
	if len(calls) == 0 {
		t.Fatal("fake binary recorded no invocations")
	}
	return calls
}

func setFakeEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestSA107_BackendIdentity(t *testing.T) {
	cases := []struct {
		v           VCS
		wantName    string
		wantDisplay string
	}{
		{Git{}, "git", "Git"},
		{Mercurial{}, "hg", "Mercurial"},
		{Subversion{}, "svn", "Subversion"},
		{Jujutsu{}, "jj", "Jujutsu"},
	}
	for _, tc := range cases {
		if got := tc.v.Name(); got != tc.wantName {
			t.Errorf("Name() = %q, want %q", got, tc.wantName)
		}
		if got := tc.v.DisplayName(); got != tc.wantDisplay {
			t.Errorf("%s.DisplayName() = %q, want %q", tc.wantName, got, tc.wantDisplay)
		}
	}
}

func TestSA107_HgCommands(t *testing.T) {
	ctx := context.Background()

	t.Run("status passthrough", func(t *testing.T) {
		dir, log := setupFakeBin(t, "hg")
		setFakeEnv(t, map[string]string{"FAKEVCS_OUT": "M f.txt"})
		out, err := (Mercurial{}).Status(ctx, dir)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if strings.TrimSpace(out) != "M f.txt" {
			t.Errorf("Status() = %q, want passthrough of fake output", out)
		}
		if got := recordedCalls(t, log)[0]; got != "status" {
			t.Errorf("calls[0] = %q, want %q", got, "status")
		}
	})

	t.Run("diff args", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "hg")
		if _, err := (Mercurial{}).Diff(ctx, dir, false, ""); err != nil {
			t.Fatalf("Diff: %v", err)
		}
		if _, err := (Mercurial{}).Diff(ctx, dir, false, "f.go"); err != nil {
			t.Fatalf("Diff(file): %v", err)
		}
		if _, err := (Mercurial{}).Diff(ctx, dir, true, "f.go"); err != nil {
			t.Fatalf("Diff(cached): %v", err)
		}
	})

	t.Run("log count and default", func(t *testing.T) {
		dir, log := setupFakeBin(t, "hg")
		if _, err := (Mercurial{}).Log(ctx, dir, 3); err != nil {
			t.Fatalf("Log(3): %v", err)
		}
		if _, err := (Mercurial{}).Log(ctx, dir, 0); err != nil {
			t.Fatalf("Log(0): %v", err)
		}
		calls := recordedCalls(t, log)
		if !strings.Contains(calls[0], "-l 3") {
			t.Errorf("calls[0] = %q, want -l 3", calls[0])
		}
		if !strings.Contains(calls[1], "-l 10") {
			t.Errorf("calls[1] = %q, want count<=0 defaulting to -l 10", calls[1])
		}
	})

	t.Run("add and commit args", func(t *testing.T) {
		dir, log := setupFakeBin(t, "hg")
		if _, err := (Mercurial{}).Add(ctx, dir, []string{"a", "b"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if _, err := (Mercurial{}).Commit(ctx, dir, "msg1"); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		calls := recordedCalls(t, log)
		if calls[0] != "add -- a b" {
			t.Errorf("Add args = %q, want %q", calls[0], "add -- a b")
		}
		if calls[1] != "commit -m msg1" {
			t.Errorf("Commit args = %q, want %q", calls[1], "commit -m msg1")
		}
	})

	t.Run("current branch trims output", func(t *testing.T) {
		dir, log := setupFakeBin(t, "hg")
		setFakeEnv(t, map[string]string{"FAKEVCS_OUT": " feature-x \n"})
		branch, err := (Mercurial{}).CurrentBranch(ctx, dir)
		if err != nil {
			t.Fatalf("CurrentBranch: %v", err)
		}
		if branch != "feature-x" {
			t.Errorf("CurrentBranch() = %q, want %q", branch, "feature-x")
		}
		if got := recordedCalls(t, log)[0]; got != "branch" {
			t.Errorf("calls[0] = %q, want %q", got, "branch")
		}
	})

	t.Run("is clean", func(t *testing.T) {
		cases := []struct {
			name string
			env  map[string]string
			want bool
		}{
			{"empty output is clean", map[string]string{"FAKEVCS_OUT": ""}, true},
			{"dirty output", map[string]string{"FAKEVCS_OUT": "M f"}, false},
			{"command failure", map[string]string{"FAKEVCS_FAIL": "1"}, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir, _ := setupFakeBin(t, "hg")
				setFakeEnv(t, tc.env)
				clean, err := (Mercurial{}).IsClean(ctx, dir)
				if tc.name == "command failure" {
					if err == nil {
						t.Fatal("IsClean: want error on command failure")
					}
					return
				}
				if err != nil {
					t.Fatalf("IsClean: %v", err)
				}
				if clean != tc.want {
					t.Errorf("IsClean() = %v, want %v", clean, tc.want)
				}
			})
		}
	})
}

func TestSA107_HgCheckout(t *testing.T) {
	ctx := context.Background()

	t.Run("switch existing", func(t *testing.T) {
		dir, log := setupFakeBin(t, "hg")
		if _, err := (Mercurial{}).Checkout(ctx, dir, "topic", false, ""); err != nil {
			t.Fatalf("Checkout: %v", err)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 1 || calls[0] != "update topic" {
			t.Errorf("calls = %v, want [update topic]", calls)
		}
	})

	t.Run("create bookmark then update", func(t *testing.T) {
		dir, log := setupFakeBin(t, "hg")
		if _, err := (Mercurial{}).Checkout(ctx, dir, "topic", true, ""); err != nil {
			t.Fatalf("Checkout(create): %v", err)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 2 || calls[0] != "bookmark topic" || calls[1] != "update topic" {
			t.Errorf("calls = %v, want [bookmark topic update topic]", calls)
		}
	})

	t.Run("create at revision", func(t *testing.T) {
		dir, log := setupFakeBin(t, "hg")
		if _, err := (Mercurial{}).Checkout(ctx, dir, "topic", true, "rev42"); err != nil {
			t.Fatalf("Checkout(create,startPoint): %v", err)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 2 || calls[0] != "bookmark topic -r rev42" || calls[1] != "update topic" {
			t.Errorf("calls = %v, want [bookmark topic -r rev42 update topic]", calls)
		}
	})

	t.Run("bookmark failure short-circuits", func(t *testing.T) {
		dir, log := setupFakeBin(t, "hg")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		if _, err := (Mercurial{}).Checkout(ctx, dir, "topic", true, ""); err == nil {
			t.Fatal("Checkout(create): want error when bookmark fails")
		}
		calls := recordedCalls(t, log)
		if len(calls) != 1 {
			t.Errorf("calls = %v, want only the bookmark call (no follow-up update)", calls)
		}
	})
}

func TestSA107_JjCommands(t *testing.T) {
	ctx := context.Background()

	t.Run("status prefers diff summary", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{"FAKEVCS_OUT": "M f"})
		out, err := (Jujutsu{}).Status(ctx, dir)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if strings.TrimSpace(out) != "M f" {
			t.Errorf("Status() = %q, want %q", out, "M f")
		}
		calls := recordedCalls(t, log)
		if len(calls) != 1 || calls[0] != "diff -r @ --summary" {
			t.Errorf("calls = %v, want single [diff -r @ --summary]", calls)
		}
	})

	t.Run("status falls back to st", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{
			"FAKEVCS_FAIL_UNTIL": "1", // first call (diff --summary) fails
			"FAKEVCS_OUT":        "M f",
		})
		out, err := (Jujutsu{}).Status(ctx, dir)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if strings.TrimSpace(out) != "M f" {
			t.Errorf("Status() = %q, want st fallback output", out)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 2 || calls[0] != "diff -r @ --summary" || calls[1] != "st" {
			t.Errorf("calls = %v, want [diff -r @ --summary st]", calls)
		}
	})

	t.Run("diff args", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "jj")
		if _, err := (Jujutsu{}).Diff(ctx, dir, true, "f.go"); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})

	t.Run("log count and default", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		if _, err := (Jujutsu{}).Log(ctx, dir, 3); err != nil {
			t.Fatalf("Log(3): %v", err)
		}
		if _, err := (Jujutsu{}).Log(ctx, dir, 0); err != nil {
			t.Fatalf("Log(0): %v", err)
		}
		calls := recordedCalls(t, log)
		if !strings.Contains(calls[0], "-n 3") || !strings.Contains(calls[0], "--no-graph") {
			t.Errorf("calls[0] = %q, want -n 3 --no-graph", calls[0])
		}
		if !strings.Contains(calls[1], "-n 10") {
			t.Errorf("calls[1] = %q, want count<=0 defaulting to -n 10", calls[1])
		}
	})

	t.Run("add tracks files", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		if _, err := (Jujutsu{}).Add(ctx, dir, []string{"a"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if got := recordedCalls(t, log)[0]; got != "file track -- a" {
			t.Errorf("Add args = %q, want %q", got, "file track -- a")
		}
	})

	t.Run("commit is describe then new", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{"FAKEVCS_OUT": "described", "FAKEVCS_OUT2": "newed"})
		out, err := (Jujutsu{}).Commit(ctx, dir, "msg1")
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if !strings.Contains(out, "described") || !strings.Contains(out, "newed") {
			t.Errorf("Commit() = %q, want concatenated describe+new output", out)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 2 || calls[0] != "describe -m msg1" || calls[1] != "new" {
			t.Errorf("calls = %v, want [describe -m msg1 new]", calls)
		}
	})

	t.Run("commit describe failure short-circuits", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		if _, err := (Jujutsu{}).Commit(ctx, dir, "msg1"); err == nil {
			t.Fatal("Commit: want error when describe fails")
		}
		if calls := recordedCalls(t, log); len(calls) != 1 {
			t.Errorf("calls = %v, want only describe", calls)
		}
	})

	t.Run("commit new failure after describe", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL_AFTER": "1"})
		if _, err := (Jujutsu{}).Commit(ctx, dir, "msg1"); err == nil {
			t.Fatal("Commit: want error when new fails")
		}
		calls := recordedCalls(t, log)
		if len(calls) != 2 || calls[1] != "new" {
			t.Errorf("calls = %v, want describe then new", calls)
		}
	})

	t.Run("current branch change id with main fallback", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{"FAKEVCS_OUT": " abc123 "})
		branch, err := (Jujutsu{}).CurrentBranch(ctx, dir)
		if err != nil || branch != "abc123" {
			t.Fatalf("CurrentBranch() = %q, %v; want abc123", branch, err)
		}

		dir2, _ := setupFakeBin(t, "jj")
		branch2, err := (Jujutsu{}).CurrentBranch(ctx, dir2)
		if err != nil || branch2 != "main" {
			t.Fatalf("CurrentBranch(empty) = %q, %v; want main fallback", branch2, err)
		}
	})

	t.Run("is clean via diff summary", func(t *testing.T) {
		cases := []struct {
			name string
			env  map[string]string
			want bool
		}{
			{"empty summary is clean", map[string]string{"FAKEVCS_OUT": ""}, true},
			{"non-empty summary is dirty", map[string]string{"FAKEVCS_OUT": "M f"}, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir, _ := setupFakeBin(t, "jj")
				setFakeEnv(t, tc.env)
				clean, err := (Jujutsu{}).IsClean(ctx, dir)
				if err != nil {
					t.Fatalf("IsClean: %v", err)
				}
				if clean != tc.want {
					t.Errorf("IsClean() = %v, want %v", clean, tc.want)
				}
			})
		}
	})

	t.Run("is clean falls back to st wording", func(t *testing.T) {
		cases := []struct {
			name string
			out  string
			want bool
		}{
			{"old wording", "The working copy has no changes", true},
			{"new wording", "The working copy is clean", true},
			{"dirty prose", "Working copy changes:\nM f", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir, _ := setupFakeBin(t, "jj")
				setFakeEnv(t, map[string]string{
					"FAKEVCS_FAIL_UNTIL": "1", // force the st fallback path
					"FAKEVCS_OUT":        tc.out,
				})
				clean, err := (Jujutsu{}).IsClean(ctx, dir)
				if err != nil {
					t.Fatalf("IsClean: %v", err)
				}
				if clean != tc.want {
					t.Errorf("IsClean() = %v, want %v", clean, tc.want)
				}
			})
		}
	})
}

func TestSA107_JjCheckout(t *testing.T) {
	ctx := context.Background()

	t.Run("switch existing runs new", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		if _, err := (Jujutsu{}).Checkout(ctx, dir, "topic", false, ""); err != nil {
			t.Fatalf("Checkout: %v", err)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 1 || calls[0] != "new topic" {
			t.Errorf("calls = %v, want [new topic]", calls)
		}
	})

	t.Run("create bookmark then switch", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{"FAKEVCS_OUT": "created", "FAKEVCS_OUT2": "switched"})
		out, err := (Jujutsu{}).Checkout(ctx, dir, "topic", true, "")
		if err != nil {
			t.Fatalf("Checkout(create): %v", err)
		}
		if !strings.Contains(out, "created") || !strings.Contains(out, "switched") {
			t.Errorf("Checkout() = %q, want concatenated bookmark+new output", out)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 2 || calls[0] != "bookmark create topic" || calls[1] != "new topic" {
			t.Errorf("calls = %v, want [bookmark create topic new topic]", calls)
		}
	})

	t.Run("create at revision", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		if _, err := (Jujutsu{}).Checkout(ctx, dir, "topic", true, "rev42"); err != nil {
			t.Fatalf("Checkout(create,startPoint): %v", err)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 2 || calls[0] != "bookmark create topic -r rev42" || calls[1] != "new topic" {
			t.Errorf("calls = %v, want [bookmark create topic -r rev42 new topic]", calls)
		}
	})

	t.Run("bookmark create failure short-circuits", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		if _, err := (Jujutsu{}).Checkout(ctx, dir, "topic", true, ""); err == nil {
			t.Fatal("Checkout(create): want error when bookmark create fails")
		}
		if calls := recordedCalls(t, log); len(calls) != 1 {
			t.Errorf("calls = %v, want only bookmark create", calls)
		}
	})

	t.Run("new failure after bookmark create", func(t *testing.T) {
		dir, log := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL_AFTER": "1"})
		if _, err := (Jujutsu{}).Checkout(ctx, dir, "topic", true, ""); err == nil {
			t.Fatal("Checkout(create): want error when new fails")
		}
		calls := recordedCalls(t, log)
		if len(calls) != 2 {
			t.Errorf("calls = %v, want bookmark create then new", calls)
		}
	})
}

func TestSA107_SvnCommands(t *testing.T) {
	ctx := context.Background()

	t.Run("status passthrough", func(t *testing.T) {
		dir, log := setupFakeBin(t, "svn")
		setFakeEnv(t, map[string]string{"FAKEVCS_OUT": "M f"})
		out, err := (Subversion{}).Status(ctx, dir)
		if err != nil || strings.TrimSpace(out) != "M f" {
			t.Fatalf("Status() = %q, %v; want passthrough", out, err)
		}
		if got := recordedCalls(t, log)[0]; got != "status" {
			t.Errorf("calls[0] = %q, want %q", got, "status")
		}
	})

	t.Run("diff args ignore cached", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "svn")
		if _, err := (Subversion{}).Diff(ctx, dir, true, "f.go"); err != nil {
			t.Fatalf("Diff: %v", err)
		}
		if _, err := (Subversion{}).Diff(ctx, dir, false, ""); err != nil {
			t.Fatalf("Diff: %v", err)
		}
	})

	t.Run("log newest-first and count default", func(t *testing.T) {
		dir, log := setupFakeBin(t, "svn")
		if _, err := (Subversion{}).Log(ctx, dir, 5); err != nil {
			t.Fatalf("Log(5): %v", err)
		}
		if _, err := (Subversion{}).Log(ctx, dir, 0); err != nil {
			t.Fatalf("Log(0): %v", err)
		}
		calls := recordedCalls(t, log)
		if !strings.HasPrefix(calls[0], "log -r HEAD:1 -l 5") {
			t.Errorf("calls[0] = %q, want log -r HEAD:1 -l 5", calls[0])
		}
		if !strings.Contains(calls[1], "-l 10") {
			t.Errorf("calls[1] = %q, want count<=0 defaulting to -l 10", calls[1])
		}
	})

	t.Run("log normalizes raw output", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "svn")
		raw := strings.Join([]string{
			"r2 | bob | 2026-01-02 | 1",
			"",
			"add feature",
			"------------------------------------------------------------------------",
			"r1 | alice | 2026-01-01 | 1",
			"",
			"initial import",
			"------------------------------------------------------------------------",
		}, "\n")
		setFakeEnv(t, map[string]string{"FAKEVCS_OUT": raw})
		out, err := (Subversion{}).Log(ctx, dir, 2)
		if err != nil {
			t.Fatalf("Log: %v", err)
		}
		want := "r2 | bob | 2026-01-02 | 1 | add feature\nr1 | alice | 2026-01-01 | 1 | initial import"
		if out != want {
			t.Errorf("Log() =\n%q\nwant\n%q", out, want)
		}
	})

	t.Run("add and commit args", func(t *testing.T) {
		dir, log := setupFakeBin(t, "svn")
		if _, err := (Subversion{}).Add(ctx, dir, []string{"a", "b"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if _, err := (Subversion{}).Commit(ctx, dir, "msg1"); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		calls := recordedCalls(t, log)
		if calls[0] != "add -- a b" {
			t.Errorf("Add args = %q, want %q", calls[0], "add -- a b")
		}
		if calls[1] != "commit -m msg1 --non-interactive" {
			t.Errorf("Commit args = %q, want %q", calls[1], "commit -m msg1 --non-interactive")
		}
	})

	t.Run("current branch url parsing", func(t *testing.T) {
		cases := []struct {
			url  string
			want string
		}{
			{"https://svn.example.com/repo/branches/feature-x", "feature-x"},
			{"https://svn.example.com/repo/branches/feature-x/", "feature-x"},
			{"https://svn.example.com/repo/trunk", "trunk"},
			{"https://svn.example.com/repo", "repo"},
		}
		for _, tc := range cases {
			t.Run(tc.url, func(t *testing.T) {
				dir, _ := setupFakeBin(t, "svn")
				setFakeEnv(t, map[string]string{"FAKEVCS_OUT": tc.url})
				got, err := (Subversion{}).CurrentBranch(ctx, dir)
				if err != nil {
					t.Fatalf("CurrentBranch: %v", err)
				}
				if got != tc.want {
					t.Errorf("CurrentBranch() = %q, want %q", got, tc.want)
				}
			})
		}
	})

	t.Run("is clean ignores externals noise", func(t *testing.T) {
		cases := []struct {
			name string
			out  string
			want bool
		}{
			{"empty", "", true},
			{"local modification", "M f", false},
			{"externals definition", "X ext", true},
			{"external fetch progress", "> fetching ext", true},
			{"recursive external walk", "Performing status on external item at 'ext'", true},
			{"unversioned still dirty", "X ext\n? new.txt", false},
			{"modification plus externals", "M f\nX ext", false},
			{"crlf line ending", "M f\r", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir, _ := setupFakeBin(t, "svn")
				setFakeEnv(t, map[string]string{"FAKEVCS_OUT": tc.out})
				clean, err := (Subversion{}).IsClean(ctx, dir)
				if err != nil {
					t.Fatalf("IsClean: %v", err)
				}
				if clean != tc.want {
					t.Errorf("IsClean(%q) = %v, want %v", tc.out, clean, tc.want)
				}
			})
		}
	})

	t.Run("is clean propagates command failure", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "svn")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		if clean, err := (Subversion{}).IsClean(ctx, dir); err == nil || clean {
			t.Errorf("IsClean() = %v, %v; want false, error", clean, err)
		}
	})

	t.Run("checkout not supported", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "svn")
		out, err := (Subversion{}).Checkout(ctx, dir, "topic", true, "")
		if !errors.Is(err, ErrCheckoutNotSupported) {
			t.Errorf("Checkout err = %v, want ErrCheckoutNotSupported", err)
		}
		if out != "" {
			t.Errorf("Checkout out = %q, want empty", out)
		}
	})
}

func TestSA107_GitCheckout(t *testing.T) {
	ctx := context.Background()

	t.Run("switch existing", func(t *testing.T) {
		dir, log := setupFakeBin(t, "git")
		if _, err := (Git{}).Checkout(ctx, dir, "topic", false, ""); err != nil {
			t.Fatalf("Checkout: %v", err)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 1 || calls[0] != "checkout topic" {
			t.Errorf("calls = %v, want [checkout topic]", calls)
		}
	})

	t.Run("create branch", func(t *testing.T) {
		dir, log := setupFakeBin(t, "git")
		if _, err := (Git{}).Checkout(ctx, dir, "topic", true, ""); err != nil {
			t.Fatalf("Checkout(create): %v", err)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 1 || calls[0] != "checkout -b topic" {
			t.Errorf("calls = %v, want [checkout -b topic]", calls)
		}
	})

	t.Run("create branch at start point", func(t *testing.T) {
		dir, log := setupFakeBin(t, "git")
		if _, err := (Git{}).Checkout(ctx, dir, "topic", true, "base"); err != nil {
			t.Fatalf("Checkout(create,startPoint): %v", err)
		}
		calls := recordedCalls(t, log)
		if len(calls) != 1 || calls[0] != "checkout -b topic base" {
			t.Errorf("calls = %v, want [checkout -b topic base]", calls)
		}
	})

	t.Run("failure propagates stderr", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "git")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		_, err := (Git{}).Checkout(ctx, dir, "topic", true, "")
		if err == nil {
			t.Fatal("Checkout: want error on command failure")
		}
		if !strings.Contains(err.Error(), "checkout -b topic") || !strings.Contains(err.Error(), "fake failure") {
			t.Errorf("err = %v, want command echo plus stderr text", err)
		}
	})
}

func TestSA107_RunVCSCmdErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("exit error includes command and stderr", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "hg")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		_, err := (Mercurial{}).Status(ctx, dir)
		if err == nil {
			t.Fatal("Status: want error")
		}
		if !strings.Contains(err.Error(), "hg status") || !strings.Contains(err.Error(), "fake failure 1") {
			t.Errorf("err = %v, want \"hg status\" echo with stderr text", err)
		}
	})

	t.Run("missing binary wraps lookup error", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("PATH manipulation requires a POSIX shell environment")
		}
		emptyDir := t.TempDir()
		t.Setenv("PATH", emptyDir) // no git available anywhere
		_, err := (Git{}).Status(ctx, t.TempDir())
		if err == nil {
			t.Fatal("Status: want error when binary is missing")
		}
		if strings.Contains(err.Error(), "fake failure") {
			t.Errorf("err = %v, want lookup error (not stderr passthrough)", err)
		}
		if !strings.Contains(err.Error(), "git status") {
			t.Errorf("err = %v, want command echo in wrapped lookup error", err)
		}
	})
}

func TestSA107_NormalizeSvnLogEdges(t *testing.T) {
	sep := "------------------------------------------------------------------------"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"header only block", "r2 | bob | d | 0", "r2 | bob | d | 0"},
		{"message after blank line", "r1 | a | d | 1\n\nfix bug", "r1 | a | d | 1 | fix bug"},
		{
			"header continuation with pipe is skipped",
			"r1 | a | d | 1\nr1 cont | x\nreal msg",
			"r1 | a | d | 1 | real msg",
		},
		{
			"message containing pipe accepted after first line",
			"r1 | a | d | 1\n\nrevert | x",
			"r1 | a | d | 1 | revert | x",
		},
		{"separator only input", sep, ""},
		{"empty input", "", ""},
		{
			"multiple blocks with trailing separator",
			"r2 | b | d | 1\n\nsecond\n" + sep + "\nr1 | a | d | 1\n\nfirst\n" + sep,
			"r2 | b | d | 1 | second\nr1 | a | d | 1 | first",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSvnLog(tc.in); got != tc.want {
				t.Errorf("normalizeSvnLog(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSA107_CurrentBranchErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("hg", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "hg")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		if _, err := (Mercurial{}).CurrentBranch(ctx, dir); err == nil {
			t.Error("CurrentBranch: want error on command failure")
		}
	})

	t.Run("jj", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "jj")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		if _, err := (Jujutsu{}).CurrentBranch(ctx, dir); err == nil {
			t.Error("CurrentBranch: want error on command failure")
		}
	})

	t.Run("svn", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "svn")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		if _, err := (Subversion{}).CurrentBranch(ctx, dir); err == nil {
			t.Error("CurrentBranch: want error on command failure")
		}
	})

	t.Run("svn log failure", func(t *testing.T) {
		dir, _ := setupFakeBin(t, "svn")
		setFakeEnv(t, map[string]string{"FAKEVCS_FAIL": "1"})
		if _, err := (Subversion{}).Log(ctx, dir, 5); err == nil {
			t.Error("Log: want error on command failure")
		}
	})
}
