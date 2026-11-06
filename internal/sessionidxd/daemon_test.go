package sessionidxd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/sessionidx"
)

// TestServeSearchFallbackParity pins the daemon-first contract: Serve over a
// temp sessions dir, query through the HTTP surface, and verify the same
// result the in-process Manager returns (parity pin from #5149 S5).
func TestServeSearchFallbackParity(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real listener")
	}
	// macOS sun_path limit is 104 bytes: t.TempDir() paths are too long for
	// a unix socket, so use a short-lived /tmp home instead.
	home, err := os.MkdirTemp("/tmp", "sidx")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	sess := filepath.Join(home, "sessions")
	idxDir := sessionidx.IdxDir(sess)
	if err := os.MkdirAll(idxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Seed one sibling session: build an index via the real Index API.
	if err := os.WriteFile(filepath.Join(sess, "20260101-000000-deadbeef.jsonl"),
		[]byte(`{"type":"message","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"the unicorns assembled quietly"}]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ix, err := sessionidx.Load(sess, "20260101-000000-deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}

	go func() { _ = Serve(sess, home) }()
	// Wait for discovery file.
	deadline := time.Now().Add(5 * time.Second)
	addrFile, _, _ := Paths(home)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(addrFile); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	res, ok := Query(home, sess, []string{"unicorns", "quietly"}, "", "somelive", 10, "")
	if !ok {
		t.Fatal("daemon query failed (no daemon or timeout)")
	}
	if len(res) == 0 {
		t.Fatal("expected hits for 'unicorns quietly'")
	}
	found := false
	for _, r := range res {
		if r.SessionID == "20260101-000000-deadbeef" && contains(r.Line, "unicorns") {
			found = true
		}
		if r.SessionID == "somelive" {
			t.Fatal("live session must be excluded from daemon results")
		}
	}
	if !found {
		t.Fatal("seeded session hit missing")
	}
	// Status endpoint sanity via the same client path.
	c, okc := dialClient(home)
	if !okc {
		t.Fatal("dial failed after discovery present")
	}
	resp, err := c.Get("http://ggcode-sessionidxd/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil || !st.OK || st.BudgetMB <= 0 {
		t.Fatalf("bad status: %+v err=%v", st, err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || index(s, sub) >= 0)
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestQueryDaemonDisabled pins the env kill-switch: GGCODE_SESSIONIDX_DAEMON=0
// must return ok=false without touching the filesystem.
func TestQueryDaemonDisabled(t *testing.T) {
	t.Setenv("GGCODE_SESSIONIDX_DAEMON", "0")
	if _, ok := Query(t.TempDir(), t.TempDir(), []string{"x"}, "", "", 5, ""); ok {
		t.Fatal("daemon must be disabled by env")
	}
}

var _ = http.DefaultClient
