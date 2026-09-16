package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
)

// initTestGitRepo creates a minimal committed git repo in a temp dir.
func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "init")
	return dir
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "add", "-A").CombinedOutput()
	if err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	out, err = exec.Command("git", "-C", dir, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "-qm", "w").CombinedOutput()
	if err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func newTestCpMgr() *checkpoint.Manager {
	return checkpoint.NewManager(50)
}

func TestShellCheckpointModifyTrackedFile(t *testing.T) {
	dir := initTestGitRepo(t)
	f := filepath.Join(dir, "main.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)

	pre := takeShellSnapshotIn(dir)
	if pre == nil {
		t.Fatal("expected snapshot in git repo")
	}

	// Shell-style mutation: append content without going through editor tools.
	if err := os.WriteFile(f, []byte("package main\n// mutated by sed -i\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := newTestCpMgr()
	n := pre.checkpointMutations(takeShellSnapshotIn(dir), "run_command", mgr)
	if n != 1 {
		t.Fatalf("got %d checkpoints, want 1", n)
	}

	cp, err := mgr.Undo("test")
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "package main\n" {
		t.Fatalf("undo restored %q, want pre-state", got)
	}
	if cp.FilePath != f || cp.ToolCall != "run_command" {
		t.Fatalf("checkpoint attribution wrong: %+v", cp)
	}
}

func TestShellCheckpointCreateAndUndoDeletes(t *testing.T) {
	dir := initTestGitRepo(t)
	pre := takeShellSnapshotIn(dir)

	gen := filepath.Join(dir, "gen.txt")
	if err := os.WriteFile(gen, []byte("codegen output\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := newTestCpMgr()
	if n := pre.checkpointMutations(takeShellSnapshotIn(dir), "run_command", mgr); n != 1 {
		t.Fatalf("got %d checkpoints, want 1", n)
	}
	cp, err := mgr.Undo("test")
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if cp.Existed {
		t.Fatal("created-file checkpoint must record Existed=false")
	}
	if _, err := os.Stat(gen); !os.IsNotExist(err) {
		t.Fatal("undo should DELETE the file the command created")
	}
}

func TestShellCheckpointDeleteAndUndoRestores(t *testing.T) {
	dir := initTestGitRepo(t)
	f := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(f, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)

	pre := takeShellSnapshotIn(dir)
	if err := os.Remove(f); err != nil { // "rm keep.txt"
		t.Fatal(err)
	}

	mgr := newTestCpMgr()
	if n := pre.checkpointMutations(takeShellSnapshotIn(dir), "run_command", mgr); n != 1 {
		t.Fatalf("got %d checkpoints, want 1", n)
	}
	if _, err := mgr.Undo("test"); err != nil {
		t.Fatalf("undo: %v", err)
	}
	got, err := os.ReadFile(f)
	if err != nil || string(got) != "precious\n" {
		t.Fatalf("undo should restore deleted file, got %q err=%v", got, err)
	}
}

func TestShellCheckpointNoMutationNoCheckpoint(t *testing.T) {
	dir := initTestGitRepo(t)
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)

	pre := takeShellSnapshotIn(dir)
	mgr := newTestCpMgr()
	// Command rewrites the file with identical content (e.g. gofmt no-op).
	if err := os.WriteFile(f, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := pre.checkpointMutations(takeShellSnapshotIn(dir), "run_command", mgr); n != 0 {
		t.Fatalf("got %d checkpoints, want 0 for no-op command", n)
	}
	if len(mgr.List()) != 0 {
		t.Fatal("undo stack must stay empty for no-op commands")
	}
}

func TestShellSnapshotNilOutsideGit(t *testing.T) {
	dir := t.TempDir() // no git init
	if s := takeShellSnapshotIn(dir); s != nil {
		t.Fatal("expected nil snapshot outside a git worktree")
	}
	// checkpointMutations with nil snapshots must be a safe no-op.
	mgr := newTestCpMgr()
	var nilSnap *shellSnapshot
	if n := nilSnap.checkpointMutations(nil, "run_command", mgr); n != 0 {
		t.Fatalf("nil-snapshot no-op returned %d", n)
	}
}

func TestSplitPorcelainZ(t *testing.T) {
	// "M  a.go\0?? new.txt\0R  renamed\0old\0" -- rename carries two fields.
	out := []byte("M  a.go\x00?? new.txt\x00R  renamed\x00old\x00")
	entries := splitPorcelainZ(out)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	if entries[0].status != "M " || entries[0].path != "a.go" {
		t.Errorf("entry0 = %+v", entries[0])
	}
	if entries[1].status != "??" || entries[1].path != "new.txt" {
		t.Errorf("entry1 = %+v", entries[1])
	}
	if entries[2].status != "R " || entries[2].path != "renamed" {
		t.Errorf("rename entry must take the NEW path: %+v", entries[2])
	}
}

func TestShellCheckpointSkipUntrackedDeps(t *testing.T) {
	dir := initTestGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(dir, "node_modules", "pkg", "x.js")
	if err := os.WriteFile(junk, []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pre := takeShellSnapshotIn(dir)
	mgr := newTestCpMgr()
	// Nothing changed except dependency noise -> no checkpoints.
	if n := pre.checkpointMutations(pre, "run_command", mgr); n != 0 {
		t.Fatalf("got %d checkpoints, want 0 for dep-dir noise", n)
	}
}

// --- #2441 review fix: oversize files must never be checkpointed ---

// oversizedContent exceeds shellCkptMaxFileBytes (1 MiB).
var oversizedContent = strings.Repeat("x", (1<<20)+64)

// TestShellCheckpointOversizePreModified: a >1MiB dirty file that a shell
// command shrinks (e.g. sed) must NOT get a checkpoint. Old behavior:
// readSnapshotFile returned ("", true), Pass 1 saved oldContent="" with
// existed=true, and undo_edit ZEROED the file on disk (real data loss).
func TestShellCheckpointOversizePreModified(t *testing.T) {
	dir := initTestGitRepo(t)
	f := filepath.Join(dir, "big.log")
	if err := os.WriteFile(f, []byte(oversizedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	pre := takeShellSnapshotIn(dir)
	if pre == nil {
		t.Fatal("expected snapshot")
	}
	if st, ok := pre.files["big.log"]; !ok || !st.existed || st.captured {
		t.Fatalf("oversize pre-state misclassified: %+v ok=%v", st, ok)
	}

	// Command shrinks it below the cap with different content.
	if err := os.WriteFile(f, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := newTestCpMgr()
	if n := pre.checkpointMutations(takeShellSnapshotIn(dir), "run_command", mgr); n != 0 {
		t.Fatalf("got %d checkpoints for oversize pre-state, want 0 (undo would zero the file)", n)
	}
	// And undoing everything else must leave the shrunk file untouched.
	got, _ := os.ReadFile(f)
	if string(got) != "small" {
		t.Fatalf("file altered: %q", got)
	}
}

// TestShellCheckpointOversizePreDeleted: deleting an uncaptured dirty file
// must not produce a checkpoint -- a deletion checkpoint with empty
// oldContent would restore an EMPTY file on undo instead of the content.
func TestShellCheckpointOversizePreDeleted(t *testing.T) {
	dir := initTestGitRepo(t)
	f := filepath.Join(dir, "big2.log")
	if err := os.WriteFile(f, []byte(oversizedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	pre := takeShellSnapshotIn(dir)
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}

	mgr := newTestCpMgr()
	if n := pre.checkpointMutations(takeShellSnapshotIn(dir), "rm", mgr); n != 0 {
		t.Fatalf("got %d checkpoints for deleted oversize file, want 0", n)
	}
}

// TestShellCheckpointOversizePost: a file the command GREW past the cap must
// not be checkpointed either -- empty newContent would zero it on redo.
func TestShellCheckpointOversizePost(t *testing.T) {
	dir := initTestGitRepo(t)
	f := filepath.Join(dir, "grow.log")
	if err := os.WriteFile(f, []byte("tiny"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)

	pre := takeShellSnapshotIn(dir)
	if err := os.WriteFile(f, []byte(oversizedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := newTestCpMgr()
	if n := pre.checkpointMutations(takeShellSnapshotIn(dir), "run_command", mgr); n != 0 {
		t.Fatalf("got %d checkpoints for oversize post-state, want 0 (redo would zero the file)", n)
	}
}
