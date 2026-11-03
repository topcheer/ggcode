package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestIssue556HardcodedPathCommentExempt(t *testing.T) {
	src := "package main\n\n// Deploy copies the binary to /home/deployer/bin/app on the server.\nfunc f() {}\n"
	if got := checkHardcodedPaths("app.go", "", src); len(got) != 0 {
		t.Fatalf("path in comment flagged: %v", got)
	}
}

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

func TestIssue556HardcodedPathDeltaStillWorks(t *testing.T) {
	old := "package main\n\nfunc f() {\n\tf, _ := os.Open(\"/home/dev/old.txt\")\n\t_ = f\n}\n"

	newC := "package main\n\nfunc f() {\n\tf, _ := os.Open(\"/home/dev/old.txt\")\n\tg, _ := os.Open(\"/home/dev/new.txt\")\n\t_, _ = f, g\n}\n"
	got := checkHardcodedPaths("app.go", old, newC)
	if len(got) == 0 {
		t.Fatal("newly introduced path not flagged in delta mode")
	}

	if got2 := checkHardcodedPaths("app.go", old, old); len(got2) != 0 {
		t.Fatalf("unchanged content flagged: %v", got2)
	}
}

func TestIssue556ExitCallCheckTombstone(t *testing.T) {

	data, err := os.ReadFile(filepath.Join("exit_call_check.go"))
	if err != nil {
		t.Skipf("cannot read exit_call_check.go: %v", err)
	}
	src := string(data)
	for _, marker := range []string{
		"isCmdPackage",
	} {
		if !strings.Contains(src, marker) {
			t.Fatalf("tombstone missing marker %q in exit_call_check.go", marker)
		}
	}
}

func TestIssue556RepetitionLongPathKeyConvergence(t *testing.T) {

	long := "/" + strings.Repeat("seg/", 30) + "main.go"

	rt := newRepetitionTracker()
	defer rt.reset()

	for i := 0; i < 3; i++ {
		rt.recordEditAttempt("edit_file", mkEditArgs556(long), true)
	}

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

	if g := rt2.recordReadAttempt(extractFileHint("read_file", mkEditArgs556(long))); g == "" {
		t.Fatal("cycle did not fire on third read after failed edits")
	}

	if g2 := rt2.recordReadAttempt(extractFileHint("read_file", mkEditArgs556(long))); g2 != "" {
		t.Fatal("cycle re-fired for the same file in the same run")
	}
}

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
