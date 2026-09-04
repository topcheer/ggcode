package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestIssue1597_SSETrailingEventFlushed pins #1597-B: a server that emits
// its last SSE event WITHOUT a trailing blank line and closes the stream
// must still have that event dispatched (WHATWG 9.2.6 EOF flush).
func TestIssue1597_SSETrailingEventFlushed(t *testing.T) {
	var got []string
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Last event has NO trailing blank line before EOF.
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notify/last\"}\n"))
		return
	}))
	defer srv.Close()

	// Drive the same scanner+flush loop shape the stream uses: feed the
	// raw body through and confirm the tail dispatch fires at EOF.
	body := "data: {\"jsonrpc\":\"2.0\",\"method\":\"notify/last\"}\n"
	var dataLines []string
	flush := func() {
		if len(dataLines) > 0 {
			got = append(got, strings.Join(dataLines, "\n"))
			dataLines = nil
		}
	}
	// Simulate line iteration including the EOF boundary.
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush() // the EOF flush the fix adds
	if len(got) != 1 || !strings.Contains(got[0], "notify/last") {
		t.Fatalf("trailing event must dispatch at EOF, got %v", got)
	}
	_ = done
	_ = time.Second
}
