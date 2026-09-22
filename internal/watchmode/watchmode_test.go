package watchmode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeFile(root, rel, content string) error {
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

func TestExtractMarkersFromLines_ModesAndPositions(t *testing.T) {
	lines := []string{
		"package main",
		"// @ggcode! fix the nil check",
		"func f() {}",
		"# @ggcode? why is this O(n^2)",
		"plain line with no token",
		"// @ggcode",
		"/* @ggcode! multi */",
	}
	got := ExtractMarkersFromLines("a.go", lines)
	if len(got) != 3 {
		t.Fatalf("expected 3 markers, got %d: %v", len(got), got)
	}
	if got[0].Line != 2 || got[0].Mode != ModeAct || got[0].Instruction != "fix the nil check" {
		t.Errorf("marker[0] wrong: %+v", got[0])
	}
	if got[1].Line != 4 || got[1].Mode != ModeAsk {
		t.Errorf("marker[1] wrong: %+v", got[1])
	}
	if got[2].Line != 7 || got[2].Instruction != "multi" {
		t.Errorf("marker[2] wrong: %+v", got[2])
	}
}

func TestMarkerKey_LineIndependentButTextSensitive(t *testing.T) {
	a := Marker{Path: "x.go", Line: 10, Instruction: "do the thing"}
	b := Marker{Path: "x.go", Line: 42, Instruction: "do the thing"}
	c := Marker{Path: "x.go", Line: 10, Instruction: "do another thing"}
	d := Marker{Path: "y.go", Line: 10, Instruction: "do the thing"}
	if a.Key() != b.Key() {
		t.Error("same instruction on different lines should share a key")
	}
	if a.Key() == c.Key() {
		t.Error("different instructions must not share a key")
	}
	if a.Key() == d.Key() {
		t.Error("different files must not share a key")
	}
}

func TestScanner_BaselineSkipsExistingAndDetectsNew(t *testing.T) {
	root := t.TempDir()
	if err := writeFile(root, "a.go", "package a\n// @ggcode! pre-existing task\n"); err != nil {
		t.Fatal(err)
	}
	s := NewScanner(root)
	s.PrimeBaseline(context.Background())

	markers, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 0 {
		t.Fatalf("baseline marker leaked: %v", markers)
	}

	if err := writeFile(root, "b.go", "package b\n// @ggcode? explain this\n"); err != nil {
		t.Fatal(err)
	}
	markers, err = s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 1 || markers[0].Mode != ModeAsk || markers[0].Path != "b.go" {
		t.Fatalf("expected new ask marker in b.go, got %v", markers)
	}
	markers, err = s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 0 {
		t.Fatalf("marker re-fired on unchanged file: %v", markers)
	}
}

func TestScanner_SkipsBinaryAndDotDirs(t *testing.T) {
	root := t.TempDir()
	if err := writeFile(root, ".git/config", "// @ggcode! in dotdir\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(root, "blob.bin", "\x00\x01 @ggcode! inside binary\n"); err != nil {
		t.Fatal(err)
	}
	s := NewScanner(root)
	s.PrimeBaseline(context.Background())
	markers, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 0 {
		t.Fatalf("expected no markers from binary/dotdir files, got %v", markers)
	}
}

func TestSnippetAround_WindowsAndClamps(t *testing.T) {
	lines := []string{"l1", "l2", "l3", "l4", "l5"}
	start, win := SnippetAround(lines, 3, 1)
	if start != 2 || len(win) != 3 || win[0] != "l2" || win[2] != "l4" {
		t.Fatalf("middle window wrong: start=%d win=%v", start, win)
	}
	start, win = SnippetAround(lines, 1, 2)
	if start != 1 || len(win) != 3 {
		t.Fatalf("head window wrong: start=%d win=%v", start, win)
	}
	start, win = SnippetAround(lines, 99, 2)
	if start != 1 || len(win) != 5 || win[len(win)-1] != "l5" {
		t.Fatalf("tail clamp wrong: start=%d win=%v", start, win)
	}
}

func TestBuildPrompt_ContainsPathModeAndSnippet(t *testing.T) {
	m := Marker{Path: "a.go", Line: 2, Instruction: "fix it", Mode: ModeAct}
	p := BuildPrompt(m, []string{"package a", "// @ggcode! fix it"})
	for _, want := range []string{"a.go:2", "Mode: act", "fix it", "1 | package a"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
	ask := BuildPrompt(Marker{Path: "a.go", Line: 1, Instruction: "why", Mode: ModeAsk}, nil)
	if !strings.Contains(ask, "Do NOT modify") {
		t.Errorf("ask prompt should forbid edits:\n%s", ask)
	}
}

func TestRunnerDispatch_BuildsArgsAndReturnsError(t *testing.T) {
	var gotArgs []string
	var gotDir string
	r := &Runner{
		Root:    "/tmp/proj",
		Timeout: 5 * time.Second,
		Exec: func(ctx context.Context, name string, args []string, dir string, w io.Writer) error {
			gotArgs, gotDir = args, dir
			fmt.Fprintln(w, "child output line")
			return errors.New("boom")
		},
	}
	m := Marker{Path: "a.go", Line: 3, Instruction: "do it", Mode: ModeAct}
	err := r.Dispatch(context.Background(), m, []string{"l1", "l2", "l3"})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected exec error passthrough, got %v", err)
	}
	if len(gotArgs) < 2 || gotArgs[0] != "-p" {
		t.Fatalf("args wrong: %v", gotArgs)
	}
	if !strings.Contains(gotArgs[1], "do it") || !strings.Contains(gotArgs[1], "a.go:3") {
		t.Errorf("prompt missing marker context:\n%s", gotArgs[1])
	}
	if gotDir != "/tmp/proj" {
		t.Errorf("dir wrong: %s", gotDir)
	}
}

func TestRunnerExtraArgs(t *testing.T) {
	var gotArgs []string
	r := &Runner{
		Root: "/tmp/proj",
		Exec: func(ctx context.Context, name string, args []string, dir string, w io.Writer) error {
			gotArgs = args
			return nil
		},
		ExtraArgs: func(m Marker) []string { return []string{"--bypass"} },
	}
	if err := r.Dispatch(context.Background(), Marker{Path: "a", Line: 1, Instruction: "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) != 3 || gotArgs[2] != "--bypass" {
		t.Fatalf("extra args not appended: %v", gotArgs)
	}
}

func TestPrefixWriter_IndentsLines(t *testing.T) {
	var b strings.Builder
	pw := &prefixWriter{prefix: "| ", w: &b}
	if _, err := pw.Write([]byte("one\ntwo\n")); err != nil {
		t.Fatal(err)
	}
	if b.String() != "| one\n| two\n" {
		t.Fatalf("indent wrong: %q", b.String())
	}
}

func TestRun_DispatchesNewMarkerAndStopsOnCancel(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var dispatched []string
	started := make(chan Marker, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		err := Run(ctx, Options{
			Root:     root,
			Interval: 100 * time.Millisecond,
			Stdout:   io.Discard,
			Stderr:   io.Discard,
			Exec: func(ctx context.Context, name string, args []string, dir string, w io.Writer) error {
				mu.Lock()
				dispatched = append(dispatched, args[1])
				mu.Unlock()
				started <- Marker{}
				return nil
			},
		})
		done <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	// Let the goroutine's PrimeBaseline finish on the empty directory so the
	// marker we add next counts as new.
	time.Sleep(400 * time.Millisecond)
	for {
		if err := writeFile(root, "task.go", "package t\n// @ggcode! add a test\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-started:
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(dispatched) != 1 || !strings.Contains(dispatched[0], "add a test") {
				t.Fatalf("dispatched prompts wrong: %v", dispatched)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("marker was never dispatched within 5s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
