package sessionidxd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/sessionidx"
)

// #5149 host pins (single-binary, in-process host): the first instance's
// StartHost binds the transport; a second instance in the same home stays
// a client (nil,nil); stopping the host frees the endpoint and the next
// StartHost re-elects; the client falls back locally when no host answers.

func seedSharedSession(t *testing.T, sess string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, sess+".jsonl")
	body := `{"type":"message","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"unicorns quietly graze here"}]}}` + "\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("seed %s: %v", sess, err)
	}
	ix, err := sessionidx.Load(dir, sess)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return dir
}

// shortHome: darwin unix sockets cap sun_path at 104 bytes; t.TempDir()
// paths are too long, so use a short /tmp root.
func shortHome(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "sidx")
	if err != nil {
		t.Fatalf("short home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func waitHostReady(t *testing.T, home string) {
	t.Helper()
	addrFile, _ := Paths(home)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(addrFile); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("host did not publish its endpoint in time")
}

func TestStartHost_ElectionAndClientQuery(t *testing.T) {
	sessions := seedSharedSession(t, "parity1")
	home := shortHome(t)

	stop, err := StartHost(sessions, home)
	if err != nil {
		t.Fatalf("first StartHost: %v", err)
	}
	if stop == nil {
		t.Fatal("first StartHost must win the election (non-nil stop)")
	}
	waitHostReady(t, home)

	// A second instance in the same home must stay a client.
	stop2, err2 := StartHost(sessions, home)
	if err2 != nil {
		t.Fatalf("second StartHost: %v", err2)
	}
	if stop2 != nil {
		stop2()
		t.Fatal("second StartHost must return nil stop (leader alive)")
	}

	// Client query through the transport returns host results.
	res, ok := Query(home, sessions, []string{"unicorns", "quietly"}, "", "somelive", 10, "")
	if !ok {
		t.Fatal("client query failed against a live host")
	}
	if len(res) == 0 {
		t.Fatal("expected hits from the host")
	}

	stop()
}

func TestStartHost_ReelectionAfterHostExit(t *testing.T) {
	sessions := seedSharedSession(t, "migrate1")
	home := shortHome(t)

	stop1, err := StartHost(sessions, home)
	if err != nil || stop1 == nil {
		t.Fatalf("first host: err=%v", err)
	}
	waitHostReady(t, home)

	// Host exits: endpoint artifacts go away.
	stop1()
	time.Sleep(50 * time.Millisecond)

	// Next instance re-elects on the reclaimed endpoint.
	stop2, err := StartHost(sessions, home)
	if err != nil {
		t.Fatalf("reelection: %v", err)
	}
	if stop2 == nil {
		t.Fatal("after the host exits, the next StartHost must win")
	}
	defer stop2()
	waitHostReady(t, home)
}

func TestQuery_NoHostFallsBackSignal(t *testing.T) {
	home := shortHome(t) // no host ever started here
	sessions := t.TempDir()
	if _, ok := Query(home, sessions, []string{"anything"}, "", "", 5, ""); ok {
		t.Fatal("Query must report ok=false when no host answers")
	}
}

func TestStartHost_DisabledByEnv(t *testing.T) {
	t.Setenv("GGCODE_SESSIONIDX_DAEMON", "0")
	sessions := seedSharedSession(t, "off1")
	stop, err := StartHost(sessions, shortHome(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if stop != nil {
		stop()
		t.Fatal("GGCODE_SESSIONIDX_DAEMON=0 must disable the host role")
	}
}
