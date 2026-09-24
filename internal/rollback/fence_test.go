package rollback

import (
	"strings"
	"testing"
	"time"
)

func TestRecordAndActivate(t *testing.T) {
	f := New()
	t0 := time.Now().Add(-3 * time.Minute)

	// Simulate: cmd1 before checkpoint, cmd2/cmd3 after.
	f.Record("run_command", "go build ./...")
	f.mu.Lock()
	f.recent[0].Time = t0
	f.mu.Unlock()
	f.Record("run_command", "git push origin main")
	f.Record("run_command", "gh pr create")

	// Restore to a checkpoint that predates cmd1: everything is fenced.
	acted := f.Activate(t0.Add(-time.Minute))
	if len(acted) != 3 {
		t.Fatalf("expected 3 fenced actions, got %d", len(acted))
	}
	if acted[0].Command != "go build ./..." || acted[2].Command != "gh pr create" {
		t.Fatalf("unexpected fence order: %v", acted)
	}

	if _, ok := f.Match("gh pr create"); !ok {
		t.Error("expected fenced command to match")
	}
	if _, ok := f.Match("git status"); ok {
		t.Error("unrecorded command must not match")
	}
}

func TestActivateUsesCheckpointTime(t *testing.T) {
	f := New()
	t0 := time.Now().Add(-2 * time.Minute)

	f.Record("run_command", "before")
	f.mu.Lock()
	f.recent[0].Time = t0
	f.mu.Unlock()
	f.Record("run_command", "after")

	// Restore to a checkpoint AFTER cmd 'before': only 'after' is fenced.
	acted := f.Activate(t0.Add(time.Minute))
	if len(acted) != 1 || acted[0].Command != "after" {
		t.Fatalf("expected only 'after' fenced, got %v", acted)
	}
	if _, ok := f.Match("before"); ok {
		t.Error("command predating the restored checkpoint must not be fenced")
	}
	if _, ok := f.Match("after"); !ok {
		t.Error("command executed after the checkpoint must be fenced")
	}
}

func TestActivateReplacesPreviousFence(t *testing.T) {
	f := New()
	f.Record("run_command", "first")
	f.Activate(time.Now().Add(-time.Hour))
	if _, ok := f.Match("first"); !ok {
		t.Fatal("expected first fence")
	}

	// A later restore with no commands since it replaces the fenced set.
	acted := f.Activate(time.Now().Add(time.Hour))
	if len(acted) != 0 {
		t.Fatalf("expected empty fence, got %v", acted)
	}
	if _, ok := f.Match("first"); ok {
		t.Error("stale fenced entry must be replaced by the newer restore")
	}
}

func TestMatchWhitespaceNormalization(t *testing.T) {
	f := New()
	f.Record("run_command", "git push   origin main")
	f.Activate(time.Now().Add(-time.Hour))

	if _, ok := f.Match("git push origin main"); !ok {
		t.Error("whitespace differences must still match")
	}
	if _, ok := f.Match("  git push origin main  "); !ok {
		t.Error("surrounding whitespace must still match")
	}
	if _, ok := f.Match("git push origin main --force"); ok {
		t.Error("different arguments must not match")
	}
	if _, ok := f.Match(""); ok {
		t.Error("empty command must never match")
	}
}

func TestRecordBounds(t *testing.T) {
	f := New()
	for i := 0; i < maxRecent+20; i++ {
		f.Record("run_command", strings.Repeat("x", 1)+" cmd-"+time.Duration(i).String())
	}
	f.mu.Lock()
	got := len(f.recent)
	f.mu.Unlock()
	if got != maxRecent {
		t.Fatalf("expected recent window capped at %d, got %d", maxRecent, got)
	}
}

func TestRecordIgnoresBlank(t *testing.T) {
	f := New()
	f.Record("run_command", "   ")
	f.Activate(time.Now().Add(-time.Hour))
	if got := f.Fenced(); len(got) != 0 {
		t.Fatalf("expected no fenced entries, got %v", got)
	}
}

func TestFencedReturnsCopy(t *testing.T) {
	f := New()
	f.Record("run_command", "cmd")
	f.Activate(time.Now().Add(-time.Hour))
	got := f.Fenced()
	got[0].Command = "mutated"
	if f.Fenced()[0].Command == "mutated" {
		t.Error("Fenced must return a copy")
	}
}
