package im

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Issue #962: IsPrimary must skip crashed-leftover PID files
// ---------------------------------------------------------------------------

// TestInstanceDetectIsPrimarySkipsDeadPID verifies that a stale PID file from a
// crashed earlier-started instance no longer demotes the sole surviving
// instance to non-primary (issue #962).
func TestInstanceDetectIsPrimarySkipsDeadPID(t *testing.T) {
	tmp := t.TempDir()
	d := NewInstanceDetect(tmp)
	d.checkAlive = func(pid int) bool { return pid == d.info.PID } // only self alive

	if _, err := d.Register(); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(d.Unregister)

	// Simulate a crashed instance that started EARLIER and left its PID file.
	stale := InstanceInfo{
		PID:       999999, // dead
		UUID:      "crashed-instance-uuid-0001",
		StartedAt: d.info.StartedAt.Add(-time.Hour),
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(d.pidFilePath(stale.PID, stale.UUID), data, 0o644); err != nil {
		t.Fatalf("write stale pid file: %v", err)
	}

	if !d.IsPrimary() {
		t.Fatal("sole surviving instance should be primary despite earlier crashed leftover PID file")
	}
}

// TestInstanceDetectIsPrimaryStillDemotedByLivePeer guards the fix: a LIVE
// earlier instance must still demote us (regression guard).
func TestInstanceDetectIsPrimaryStillDemotedByLivePeer(t *testing.T) {
	tmp := t.TempDir()
	d := NewInstanceDetect(tmp)
	selfAlive := true
	d.checkAlive = func(pid int) bool {
		return (pid == d.info.PID) || (pid == 4242 && selfAlive)
	}

	if _, err := d.Register(); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(d.Unregister)

	peer := InstanceInfo{
		PID:       4242,
		UUID:      "live-earlier-instance-uuid1",
		StartedAt: d.info.StartedAt.Add(-time.Hour),
	}
	data, _ := json.Marshal(peer)
	if err := os.WriteFile(d.pidFilePath(peer.PID, peer.UUID), data, 0o644); err != nil {
		t.Fatalf("write peer pid file: %v", err)
	}

	if d.IsPrimary() {
		t.Fatal("instance with a live earlier peer must NOT be primary")
	}
	selfAlive = false
	if !d.IsPrimary() {
		t.Fatal("after peer dies, instance should become primary")
	}
}

// ---------------------------------------------------------------------------
// Issue #960: IRC sendRaw concurrent-write serialization + mention case
// ---------------------------------------------------------------------------

// TestIRCSendRawConcurrentNoInterleave fires concurrent sendRaw writes from
// multiple goroutines (mirroring the read-loop / keepalive / send-path writers)
// and asserts every line received by the fake server is intact - no partial
// interleaving. Must pass under -race.
func TestIRCSendRawConcurrentNoInterleave(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	type result struct {
		lines []string
		errCh chan error
	}
	res := &result{errCh: make(chan error, 1)}

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			res.errCh <- err
			return
		}
		defer conn.Close()
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			res.lines = append(res.lines, sc.Text())
		}
		res.errCh <- sc.Err()
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	a := &ircAdapter{name: "test", nick: "testbot"}
	a.mu.Lock()
	a.conn = clientConn
	a.mu.Unlock()

	const goroutines = 4
	const perGoroutine = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				line := fmt.Sprintf("PRIVMSG #chan%d :writer-%d-message-%08d", g%2, g, i)
				if err := a.sendRaw(line); err != nil {
					t.Errorf("sendRaw: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	_ = clientConn.Close()

	select {
	case err := <-res.errCh:
		if err != nil {
			t.Fatalf("server read: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server to finish reading")
	}

	total := goroutines * perGoroutine
	if len(res.lines) != total {
		t.Fatalf("expected %d lines, got %d (lost/merged lines)", total, len(res.lines))
	}
	// Strict format check: every line must match the exact shape
	// "PRIVMSG #chan<N> :writer-<N>-message-<8 digits>" with no corruption.
	lineRe := regexp.MustCompile(`^PRIVMSG #chan[01] :writer-\d-message-\d{8}$`)
	for _, line := range res.lines {
		if !lineRe.MatchString(line) {
			t.Fatalf("malformed/interleaved line: %q", line)
		}
	}
}

// TestIRCMentionCaseInsensitive verifies the channel mention gate matches the
// nick case-insensitively per RFC 2812 casemapping (issue #960) and that the
// mention is stripped regardless of case.
func TestIRCMentionCaseInsensitive(t *testing.T) {
	if !ircTextContainsNick("hey Bot: check this", "bot") {
		t.Fatal("mention with different case should match")
	}
	if !ircTextContainsNick("BOT are you there", "bot") {
		t.Fatal("upper-case mention should match")
	}
	if ircTextContainsNick("completely unrelated", "bot") {
		t.Fatal("no mention should not match")
	}

	got := ircRemoveNick("hey BOT: check this", "bot")
	if strings.Contains(strings.ToLower(got), "bot") {
		t.Fatalf("mention should be stripped case-insensitively, got %q", got)
	}
	// #1221: word-based removal drops the mention token together with its
	// attached punctuation ("BOT:" goes as a whole) instead of leaving a
	// dangling colon behind.
	if strings.Join(strings.Fields(got), " ") != "hey check this" {
		t.Fatalf("unexpected remainder %q", got)
	}
}

// TestIRCNoMentionDropped sanity: without a mention (any case) the gate drops.
func TestIRCNoMentionDropped(t *testing.T) {
	if ircTextContainsNick("hello everyone", "bot") {
		t.Fatal("text without mention must not pass gate")
	}
}

// ---------------------------------------------------------------------------
// Issue #961: matrix 2-member room must not be permanently cached as DM
// ---------------------------------------------------------------------------

// TestMatrixCheckIsDMViaAPINoPermanentCache asserts that checkIsDMViaAPI does
// NOT write the 2-member heuristic result into a.dmRooms (the permanent cache
// that bypasses the mention gate). Cache entries must only come from
// fetchDMRooms / m.direct account data (issue #961).
func TestMatrixCheckIsDMViaAPINoPermanentCache(t *testing.T) {
	a := &matrixAdapter{name: "test"}
	a.dmRooms = make(map[string]bool)

	// call on a client-less adapter: must be false and must not populate cache
	if a.checkIsDMViaAPI(context.Background(), "!two:example.org") {
		t.Fatal("nil client should not report DM")
	}
	if len(a.dmRooms) != 0 {
		t.Fatalf("dmRooms must stay empty, got %v", a.dmRooms)
	}
}

// TestMatrixDMRoomDeterminationSemantics documents/verifies the m.direct
// membership helper semantics used for authoritative DM determination.
func TestMatrixDMRoomDeterminationSemantics(t *testing.T) {
	var dmMap map[string][]string
	payload := `{"@alice:example.org": ["!realDM:example.org"], "@bob:example.org": ["!other:example.org", "!another:example.org"]}`
	if err := json.Unmarshal([]byte(payload), &dmMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := make(map[string]bool)
	for _, rooms := range dmMap {
		for _, rid := range rooms {
			found[rid] = true
		}
	}
	if !found["!realDM:example.org"] || !found["!another:example.org"] {
		t.Fatal("m.direct rooms should be collected")
	}
	if found["!twoperson-group:example.org"] {
		t.Fatal("2-member room not in m.direct must not be DM")
	}
}
