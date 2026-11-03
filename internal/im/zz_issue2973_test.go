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

func TestIssue2973ReplaySinceWindow(t *testing.T) {
	b := newSSEBroker(8)
	for i := 0; i < 5; i++ {
		b.push("tick", []byte(fmt.Sprintf(`{"n":%d}`, i)))
	}
	got := b.replaySince(2)
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

	b := &sseBroker{
		buffer:  make([]sseEntry, 8),
		bufSize: 8,
		subs:    make(map[chan sseEntry]struct{}),
	}
	for i := 0; i < 13; i++ {
		b.push("tick", []byte(fmt.Sprintf(`{"n":%d}`, i)))
	}
	got := b.replaySince(0)
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

	s.sseBroker.push("already_seen", []byte(`{"old":true}`))
	s.sseBroker.push("tool_result", []byte(`{"t":2}`))

	client := util.NewInsecureAwareClient(5 * time.Second)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events", nil)
	req.Header.Set("Last-Event-ID", "1")
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

		if strings.Contains(line, `"old":true`) {
			t.Fatalf("#2973: pre-since event must not be replayed")
		}
		if strings.Contains(line, `"t":2`) {
			sawReplay = true
			cancel()
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
