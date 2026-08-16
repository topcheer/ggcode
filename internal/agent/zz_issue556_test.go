package agent

// Feature tests for GitHub issue #556 (agent-side fixes).
//   A: hardcoded_path URL-route false positive + comment stripping
//   B: exit_call_check tombstone presence (dead-code documentation)
//   C: repetition tracker edit/read key convergence on >80-rune paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- Bug A: hardcoded path false positives ----

// #556 A: URL route literals in route-registration context must NOT be
// flagged as machine-specific home paths (contradicted the #516 zero-FP claim).
func TestIssue556HardcodedPathURLRouteExempt(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "net/http HandleFunc",
			src:  "package main\n\nimport \"net/http\"\n\nfunc routes() {\n\thttp.HandleFunc(\"/home/dashboard\", handler)\n}\n",
		},
		{
			name: "gin router group",
			src:  "package main\n\nfunc routes(r *gin.Engine) {\n\tr.GET(\"/home/dashboard\", handler)\n}\n",
		},
		{
			name: "echo group",
			src:  "package main\n\nfunc routes(g *echo.Group) {\n\tg.POST(\"/home/login\", handler)\n}\n",
		},
		{
			name: "mux Handle",
			src:  "package main\n\nfunc routes(m *http.ServeMux) {\n\tm.Handle(\"/home/settings\", h)\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkHardcodedPaths("app.go", "", tc.src)
			for _, w := range got {
				if strings.Contains(w, "home path") {
					t.Fatalf("URL route literal falsely flagged as machine-specific home path:\nsource: %s\nwarning: %s", tc.src, w)
				}
			}
		})
	}
}

// #556 A: paths in // comments must not trigger warnings (prose, not code).
func TestIssue556HardcodedPathCommentExempt(t *testing.T) {
	src := "package main\n\n// Deploy copies the binary to /home/deployer/bin/app on the server.\nfunc f() {}\n"
	if got := checkHardcodedPaths("app.go", "", src); len(got) != 0 {
		t.Fatalf("path in comment flagged: %v", got)
	}
}

// #556 A regression guard: real filesystem paths MUST still be reported.
func TestIssue556HardcodedPathRealPathStillReported(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "os.Open linux home",
			src:  "package main\n\nfunc f() {\n\tf, _ := os.Open(\"/home/devuser/notes.txt\")\n\t_ = f\n}\n",
			want: "Linux home path",
		},
		{
			name: "os.Open macos home",
			src:  "package main\n\nfunc f() {\n\tf, _ := os.Open(\"/Users/john/notes.txt\")\n\t_ = f\n}\n",
			want: "macOS home path",
		},
		{
			name: "http word inside path value does not exempt",
			src:  "package main\n\nfunc f() {\n\tf, _ := os.Open(\"/home/us/http-cache/file\")\n\t_ = f\n}\n",
			want: "Linux home path",
		},
		{
			name: "path used in struct literal, no route context",
			src:  "package main\n\nvar cfg = struct{ Dir string }{Dir: \"/root/svc/data\"}\n",
			want: "Linux root home path",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkHardcodedPaths("app.go", "", tc.src)
			found := false
			for _, w := range got {
				if strings.Contains(w, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("real filesystem path NOT reported (want %q), warnings: %v", tc.want, got)
			}
		})
	}
}

// #556 A: delta logic must still work with the new matching (pre-existing
// paths are not flagged; newly introduced ones are).
func TestIssue556HardcodedPathDeltaStillWorks(t *testing.T) {
	old := "package main\n\nfunc f() {\n\tf, _ := os.Open(\"/home/dev/old.txt\")\n\t_ = f\n}\n"
	// Same file with an ADDITIONAL real path.
	newC := "package main\n\nfunc f() {\n\tf, _ := os.Open(\"/home/dev/old.txt\")\n\tg, _ := os.Open(\"/home/dev/new.txt\")\n\t_, _ = f, g\n}\n"
	got := checkHardcodedPaths("app.go", old, newC)
	if len(got) == 0 {
		t.Fatal("newly introduced path not flagged in delta mode")
	}
	// No delta when nothing new.
	if got2 := checkHardcodedPaths("app.go", old, old); len(got2) != 0 {
		t.Fatalf("unchanged content flagged: %v", got2)
	}
}

// ---- Bug B: tombstone on dead detector ----

// #556 B: exit_call_check.go must carry the #556 tombstone documenting its
// dead-code status and the two resurrection preconditions.
func TestIssue556ExitCallCheckTombstone(t *testing.T) {
	// test runs in the same package dir; find the file relative to CWD.
	data, err := os.ReadFile(filepath.Join("exit_call_check.go"))
	if err != nil {
		t.Skipf("cannot read exit_call_check.go: %v", err)
	}
	src := string(data)
	for _, marker := range []string{
		"TOMBSTONE (#556)",
		"zero production call sites",
		"func (a *App) main()", // resurrection defect 1
		"d.Recv == nil",
		"isCmdPackage", // resurrection defect 2
	} {
		if !strings.Contains(src, marker) {
			t.Fatalf("tombstone missing marker %q in exit_call_check.go", marker)
		}
	}
}

// ---- Bug C: repetition tracker key convergence ----

// #556 C: the edit side (full path from extractFilePathFromArgs) and the
// read side (truncated hint, 77 runes + "...", from overseer.extractFileHint)
// must converge on the same key for >80-rune paths. Probe showed the cycle
// detector never fired (one.go=3 reads after failed edits, zero warnings).
func TestIssue556RepetitionLongPathKeyConvergence(t *testing.T) {
	// Build a path well over 80 runes.
	long := "/" + strings.Repeat("seg/", 30) + "main.go" // 4*30+7 = 127 runes

	rt := newRepetitionTracker()
	defer rt.reset()

	// Simulate three failed edits (edit side sees the full path).
	for i := 0; i < 3; i++ {
		rt.recordEditAttempt("edit_file", mkEditArgs556(long), true)
	}
	// Simulate the read side as the agent loop feeds it: extractFileHint
	// truncates >80-rune paths to 77 runes + "...".
	hint := extractFileHint("read_file", mkEditArgs556(long))
	if hint == "" {
		t.Fatal("extractFileHint returned empty for long path")
	}
	if !strings.HasSuffix(hint, "...") {
		t.Fatalf("expected truncated hint with '...' suffix, got %q", hint)
	}
	guidance := ""
	for i := 0; i < 3; i++ {
		guidance = rt.recordReadAttempt(hint)
	}
	if guidance == "" {
		t.Fatalf("read-edit-fail cycle NOT detected for >80-rune path %q (read key must converge with edit key)", long)
	}

	// Sanity: one read before threshold does not fire.
	rt2 := newRepetitionTracker()
	defer rt2.reset()
	for i := 0; i < 3; i++ {
		rt2.recordEditAttempt("edit_file", mkEditArgs556(long), true)
	}
	if g := rt2.recordReadAttempt(extractFileHint("read_file", mkEditArgs556(long))); g != "" {
		t.Fatal("cycle fired after a single read; threshold regression")
	}
	if g := rt2.recordReadAttempt(extractFileHint("read_file", mkEditArgs556(long))); g != "" {
		t.Fatal("cycle fired after two reads; threshold regression")
	}
	// The two no-fire reads above consumed 2 of the 3 reads needed, so the
	// next read (third overall) must fire the cycle guidance.
	if g := rt2.recordReadAttempt(extractFileHint("read_file", mkEditArgs556(long))); g == "" {
		t.Fatal("cycle did not fire on third read after failed edits")
	}
	// The ":cycle" level must not re-fire within the same run.
	if g2 := rt2.recordReadAttempt(extractFileHint("read_file", mkEditArgs556(long))); g2 != "" {
		t.Fatal("cycle re-fired for the same file in the same run")
	}
}

// #556 C: short paths keep exact behavior (no truncation, keys equal).
func TestIssue556RepetitionShortPathUnchanged(t *testing.T) {
	rt := newRepetitionTracker()
	defer rt.reset()
	for i := 0; i < 3; i++ {
		rt.recordEditAttempt("edit_file", mkEditArgs556("./main.go"), true)
	}
	var g string
	for i := 0; i < 3; i++ {
		g = rt.recordReadAttempt(extractFileHint("read_file", mkEditArgs556("main.go")))
	}
	if g == "" {
		t.Fatal("cycle not detected for short path (./ stripping + lowercase must still converge)")
	}
}

// mkEditArgs556 builds edit_file-style tool arguments JSON.
func mkEditArgs556(path string) []byte {
	return []byte(`{"file_path":` + quoteJSON556(path) + `}`)
}

func quoteJSON556(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
