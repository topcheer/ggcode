package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// #1831 cases 1+2 pin: an external write between speculation and hit is a
// MISS (was: served for up to 30s and then memoized as immortal).
func Test1831SpeculatorFreshness(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte("v0"), 0o644)
	args, _ := json.Marshal(map[string]any{"file_path": p, "limit": 100})

	sp := newSpeculator()
	sp.store("read_file", args, tool.Result{Content: "content of v0"})

	if _, ok := sp.getCached("read_file", args); !ok {
		t.Fatal("unchanged file must stay a hit")
	}
	// External write: miss even well within the 30s TTL.
	os.WriteFile(p, []byte("v1-external"), 0o644)
	if _, ok := sp.getCached("read_file", args); ok {
		t.Fatal("external write must invalidate the speculative hit (TTL alone was not freshness)")
	}
}

// #1831 case 3 pin: hasCached re-verifies freshness before the pre-execution
// batch decides to skip a would-be-fresh execution.
func Test1831HasCachedFreshness(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "g.txt")
	os.WriteFile(p, []byte("v0"), 0o644)
	args, _ := json.Marshal(map[string]any{"file_path": p})

	sp := newSpeculator()
	sp.store("read_file", args, tool.Result{Content: "v0"})
	if !sp.hasCached("read_file", args) {
		t.Fatal("precondition: unchanged entry must report cached")
	}
	os.WriteFile(p, []byte("changed"), 0o644)
	if sp.hasCached("read_file", args) {
		t.Fatal("hasCached must re-verify freshness before suppressing pre-execution")
	}
}

// #1831 sentinel pins: appearing-since-speculation is drift; still-absent is not.
func Test1831SpeculatorAbsenceSemantics(t *testing.T) {
	dir := t.TempDir()
	// Appeared after speculation.
	p := filepath.Join(dir, "new.txt")
	args, _ := json.Marshal(map[string]any{"file_path": p})
	sp := newSpeculator()
	sp.store("read_file", args, tool.Result{Content: "was absent"})
	os.WriteFile(p, []byte("now here"), 0o644)
	if _, ok := sp.getCached("read_file", args); ok {
		t.Fatal("file appearing after speculation must read as drift")
	}
	// Still absent: no false drift.
	p2 := filepath.Join(dir, "never.txt")
	args2, _ := json.Marshal(map[string]any{"file_path": p2})
	sp2 := newSpeculator()
	sp2.store("read_file", args2, tool.Result{Content: "absent"})
	if _, ok := sp2.getCached("read_file", args2); !ok {
		t.Fatal("still-absent file must remain a hit (no false drift)")
	}
}
