package im

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// zz_issue2973_test.go - regression probes for #2973: the sseBroker kept a
// replay-style ring buffer (monotonic seq, hello last_seq) but NOTHING ever
// read the buffer back - no Last-Event-ID handling, no ?since=, slow
// subscribers dropped. The hello contract implied at-least-once delivery
// while the implementation was best-effort with a dead buffer. The broker
// must now replay buffered entries with seq > since, and /events must honor
// the SSE-standard Last-Event-ID header (query fallback) by replaying before
// the live loop, deduped by seq.

func TestIssue2973ReplaySinceWindow(t *testing.T) {
	b := newSSEBroker(8)
	for i := 0; i < 5; i++ {
		b.push("tick", []byte(fmt.Sprintf(`{"n":%d}`, i)))
	}
	got := b.replaySince(2) // entries with seq 3,4,5
	if len(got) != 3 {
		t.Fatalf("#2973: want 3 replayed entries, got %d", len(got))
	}
	for i, e := range got {
		if e.seq != int64(3+i) {
			t.Fatalf("#2973: replay order broken, pos %d seq %d", i, e.seq)
		}
	}
	if r := b.replaySince(5); len(r) != 0 {
		t.Fatalf("#2973: since=head must replay nothing, got %d", len(r))
	}
}

func TestIssue2973ReplayRingWraparound(t *testing.T) {
	// Construct the broker directly so the 8-slot ring is honored - the
	// constructor floors bufSize at 16, which would mask the wraparound.
	b := &sseBroker{
		buffer:  make([]sseEntry, 8),
		bufSize: 8,
		subs:    make(map[chan sseEntry]struct{}),
	}
	for i := 0; i < 13; i++ { // wraps the 8-slot ring
		b.push("tick", []byte(fmt.Sprintf(`{"n":%d}`, i)))
	}
	got := b.replaySince(0) // window start is seq 6; must return 6..13 in order
	if len(got) != 8 {
		t.Fatalf("#2973: wrapped window must hold 8, got %d", len(got))
	}
	for i, e := range got {
		if e.seq != int64(6+i) {
			t.Fatalf("#2973: wraparound order broken, pos %d seq %d", i, e.seq)
		}
	}
}

func TestIssue2973LastEventIDReplayedOnConnect(t *testing.T) {
	s := newHTTPServer(&dummyAdapter{})
	srv := httptest.NewServer(http.HandlerFunc(s.handleEvents))
	defer srv.Close()

	// One event lands before any subscriber connects: it becomes seq 1.
	// A later reconnecting client that saw up to seq 1 (Last-Event-ID: 1)
	// must receive hello (last_seq=1) and then the seq-2 event replayed.
	s.sseBroker.push("already_seen", []byte(`{"old":true}`))
	s.sseBroker.push("tool_result", []byte(`{"t":2}`))

	client := util.NewInsecureAwareClient(5 * time.Second)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events", nil)
	req.Header.Set("Last-Event-ID", "1") // client saw only seq 1 (hello era)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("#2973: GET /events: %v", err)
	}
	defer resp.Body.Close()

	sawHello, sawReplay := false, false
	sc := bufio.NewScanner(resp.Body)
	sc.Split(bufio.ScanLines)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: hello") {
			sawHello = true
		}
		// The replayed tool_result (seq 2) proves Last-Event-ID=1 replayed the
		// post-since event; already_seen (seq 1) must NOT come back.
		if strings.Contains(line, `"old":true`) {
			t.Fatalf("#2973: pre-since event must not be replayed")
		}
		if strings.Contains(line, `"t":2`) {
			sawReplay = true
			cancel() // done reading
			break
		}
	}
	if !sawHello {
		t.Fatalf("#2973: hello event missing")
	}
	if !sawReplay {
		t.Fatalf("#2973: Last-Event-ID=1 did not replay seq-2 round_done event")
	}
}
