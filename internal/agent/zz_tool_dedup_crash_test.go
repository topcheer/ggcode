package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func withCrashSidecarDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := journalDirFunc
	journalDirFunc = func() string { return dir }
	t.Cleanup(func() { journalDirFunc = old })
	return dir
}

func writeSidecar(t *testing.T, sessionID string, calls []crashMutatingCall) {
	t.Helper()
	var b strings.Builder
	for _, c := range calls {
		line, _ := json.Marshal(c)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(crashSidecarPath(sessionID), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCrashDedupSuppressesReplayedCall(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-supp"
	writeSidecar(t, sid, []crashMutatingCall{
		{Name: "im", Args: `{"adapter":"dd","message":"deploy done"}`, At: time.Now().Add(-2 * time.Minute)},
	})
	l := newToolDedupLedger()
	if n := SeedCrashDedup(&Agent{sessionID: sid, toolDedup: l}, sid); n != 1 {
		t.Fatalf("seeded = %d, want 1", n)
	}
	res := l.suppressDuplicate("im", `{"adapter":"dd","message":"deploy done"}`)
	if res == nil {
		t.Fatal("replayed call not suppressed")
	}
	if res.IsError {
		t.Fatal("suppression must be advisory, not error")
	}
	if !strings.Contains(res.Content, "[crash-window dedup]") {
		t.Fatalf("advisory prefix missing: %q", res.Content)
	}
	if !strings.Contains(res.Content, `"im"`) {
		t.Fatalf("advisory should name the tool: %q", res.Content)
	}
}

func TestCrashDedupWindowExpiry(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-old"
	writeSidecar(t, sid, []crashMutatingCall{
		{Name: "im", Args: `{"m":"x"}`, At: time.Now().Add(-31 * time.Minute)},
	})
	l := newToolDedupLedger()
	if n := SeedCrashDedup(&Agent{sessionID: sid, toolDedup: l}, sid); n != 0 {
		t.Fatalf("seeded = %d, want 0 (outside window)", n)
	}
	if res := l.suppressDuplicate("im", `{"m":"x"}`); res != nil {
		t.Fatalf("expired entry suppressed: %v", res)
	}
}

func TestCrashDedupEpochFreeFingerprint(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-epoch"
	writeSidecar(t, sid, []crashMutatingCall{
		{Name: "im", Args: `{"m":"push"}`, At: time.Now().Add(-3 * time.Minute)},
	})
	l := newToolDedupLedger()
	l.record("edit_file", `{"path":"a.go"}`, tool.Result{Content: "edited"})
	SeedCrashDedup(&Agent{sessionID: sid, toolDedup: l}, sid)
	if res := l.suppressDuplicate("im", `{"m":"push"}`); res == nil {
		t.Fatal("epoch bump masked crash-window replay")
	}
}

func TestCrashDedupTornTailTolerated(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-torn"
	good, _ := json.Marshal(crashMutatingCall{Name: "im", Args: `{"m":"y"}`, At: time.Now().Add(-time.Minute)})
	torn := `{"n":"im","a":"{broken`
	if err := os.WriteFile(crashSidecarPath(sid), append(append(good, '\n'), []byte(torn)...), 0o644); err != nil {
		t.Fatal(err)
	}
	l := newToolDedupLedger()
	if n := SeedCrashDedup(&Agent{sessionID: sid, toolDedup: l}, sid); n != 1 {
		t.Fatalf("seeded = %d, want 1 intact line", n)
	}
}

func TestCrashDedupSeedOneShot(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-once"
	writeSidecar(t, sid, []crashMutatingCall{
		{Name: "im", Args: `{"m":"z"}`, At: time.Now().Add(-time.Minute)},
	})
	l := newToolDedupLedger()
	SeedCrashDedup(&Agent{sessionID: sid, toolDedup: l}, sid)
	if _, err := os.Stat(crashSidecarPath(sid)); !os.IsNotExist(err) {
		t.Fatal("sidecar not consumed after seed")
	}
	if n := SeedCrashDedup(&Agent{sessionID: sid, toolDedup: l}, sid); n != 0 {
		t.Fatalf("second seed = %d, want 0", n)
	}
}

func TestCrashSidecarClearedOnJournalLifecycle(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-lifecycle"
	if err := os.WriteFile(crashSidecarPath(sid), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	MarkCompleted(sid, true, 1, 0)
	if _, err := os.Stat(crashSidecarPath(sid)); !os.IsNotExist(err) {
		t.Fatal("MarkCompleted did not clear sidecar")
	}
	os.WriteFile(crashSidecarPath(sid), []byte("{}\n"), 0o644)
	MarkRunning(sid, "fresh run", os.Getpid())
	if _, err := os.Stat(crashSidecarPath(sid)); !os.IsNotExist(err) {
		t.Fatal("MarkRunning did not clear stale sidecar")
	}
}

func TestAppendCrashSidecarGating(t *testing.T) {
	withCrashSidecarDir(t)
	sid := "crash-append"
	t.Setenv("GGCODE_TOOL_DEDUP", "")
	a := &Agent{sessionID: sid, toolDedup: newToolDedupLedger()}
	a.appendCrashSidecar("read_file", `{}`)
	a.appendCrashSidecar("im", `{"m":"1"}`)
	data, err := os.ReadFile(crashSidecarPath(sid))
	if err != nil {
		t.Fatal("sidecar not created for mutating call")
	}
	if !strings.Contains(string(data), `"im"`) {
		t.Fatalf("mutating call missing from sidecar: %s", data)
	}
	if strings.Contains(string(data), "read_file") {
		t.Fatal("non-mutating tool leaked into sidecar")
	}
}
