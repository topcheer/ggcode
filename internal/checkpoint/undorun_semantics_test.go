package checkpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUndoRunRefusesWhenRunBaselineEvicted(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(fp, []byte("v0"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(3)
	m.StartRun("run-1")
	for i := 0; i < 5; i++ {
		oldV, newV := fmt.Sprintf("v%d", i), fmt.Sprintf("v%d", i+1)
		if err := os.WriteFile(fp, []byte(newV), 0644); err != nil {
			t.Fatal(err)
		}
		m.Save(fp, oldV, newV, "edit_file")
	}

	reverted, err := m.UndoRun()
	if err == nil {
		t.Fatalf("UndoRun must refuse when the run's baseline was evicted; got success with %d reverts", len(reverted))
	}
	if !strings.Contains(err.Error(), "evicted") {
		t.Errorf("error should explain the eviction: %v", err)
	}
	if len(reverted) != 0 {
		t.Errorf("refusal must not partially revert; got %d reverts", len(reverted))
	}
	data, _ := os.ReadFile(fp)
	if string(data) != "v5" {
		t.Errorf("file must stay at v5 after refusal; got %q (silently rolled back to mid-run state)", string(data))
	}
	if m.CanRedo() {
		t.Error("refusal must not touch the redo stack")
	}
	if got := len(m.List()); got != 3 {
		t.Errorf("checkpoints should be retained after refusal; got %d", got)
	}
}

func TestUndoRunEvictionSkipsCurrentRun(t *testing.T) {
	dir := t.TempDir()
	x := filepath.Join(dir, "x.txt")
	y := filepath.Join(dir, "y.txt")
	os.WriteFile(x, []byte("x0"), 0644)
	os.WriteFile(y, []byte("y0"), 0644)

	m := NewManager(3)
	m.StartRun("run-0")
	os.WriteFile(x, []byte("x1"), 0644)
	m.Save(x, "x0", "x1", "edit_file")
	os.WriteFile(x, []byte("x2"), 0644)
	m.Save(x, "x1", "x2", "edit_file")

	m.StartRun("run-1")
	for i := 0; i < 3; i++ {
		oldV, newV := fmt.Sprintf("y%d", i), fmt.Sprintf("y%d", i+1)
		if err := os.WriteFile(y, []byte(newV), 0644); err != nil {
			t.Fatal(err)
		}
		m.Save(y, oldV, newV, "edit_file")
	}

	reverted, err := m.UndoRun()
	if err != nil {
		t.Fatalf("UndoRun should succeed when the tail run was not split: %v", err)
	}
	if len(reverted) != 1 {
		t.Fatalf("expected 1 reverted file, got %d", len(reverted))
	}
	data, _ := os.ReadFile(y)
	if string(data) != "y0" {
		t.Errorf("y must roll back to pre-run baseline y0; got %q", string(data))
	}
	dx, _ := os.ReadFile(x)
	if string(dx) != "x2" {
		t.Errorf("x must stay at x2; got %q", string(dx))
	}
}

func TestUndoRunPartialFailureCleansAllRunCheckpoints(t *testing.T) {
	goodDir := t.TempDir()
	badDir := t.TempDir()
	a := filepath.Join(goodDir, "a.txt")
	bad := filepath.Join(badDir, "b.txt")
	if err := os.WriteFile(a, []byte("v0"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("b0"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(50)
	m.StartRun("run-1")
	if err := os.WriteFile(bad, []byte("b1"), 0644); err != nil {
		t.Fatal(err)
	}
	m.Save(bad, "b0", "b1", "edit_file")
	for i := 0; i < 3; i++ {
		oldV, newV := fmt.Sprintf("v%d", i), fmt.Sprintf("v%d", i+1)
		if err := os.WriteFile(a, []byte(newV), 0644); err != nil {
			t.Fatal(err)
		}
		m.Save(a, oldV, newV, "edit_file")
	}

	if err := os.Chmod(badDir, 0555); err != nil {
		t.Skipf("cannot make bad dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(badDir, 0755) })
	probe := filepath.Join(badDir, ".writeprobe")
	if err := os.WriteFile(probe, nil, 0644); err == nil {
		_ = os.Remove(probe)
		t.Skip("directory write protection not enforced (running as root?)")
	}

	reverted, err := m.UndoRun()
	if err == nil {
		t.Fatal("UndoRun should fail on the unwritable directory")
	}
	if len(reverted) != 1 || reverted[0].FilePath != a {
		t.Fatalf("expected exactly a.txt to be reverted before the failure; got %+v", reverted)
	}
	data, _ := os.ReadFile(a)
	if string(data) != "v0" {
		t.Errorf("a must be rolled back to v0; got %q", string(data))
	}

	for _, cp := range m.List() {
		if cp.FilePath == a {
			t.Fatalf("residual checkpoint for reverted file %s: %+v", a, cp)
		}
	}

	if _, err := m.Undo("user"); err == nil {
		t.Fatal("Undo should fail while the bad dir is read-only")
	}
	data, _ = os.ReadFile(a)
	if string(data) != "v0" {
		t.Errorf("a must still be at v0 after the later Undo; got %q (mid-run state re-applied)", string(data))
	}
}
