package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIssue1597_SSETrailingEventFlushed(t *testing.T) {
	var got []string
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notify/last\"}\n"))
		return
	}))
	defer srv.Close()

	body := "data: {\"jsonrpc\":\"2.0\",\"method\":\"notify/last\"}\n"
	var dataLines []string
	flush := func() {
		if len(dataLines) > 0 {
			got = append(got, strings.Join(dataLines, "\n"))
			dataLines = nil
		}
	}

	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	if len(got) != 1 || !strings.Contains(got[0], "notify/last") {
		t.Fatalf("trailing event must dispatch at EOF, got %v", got)
	}
	_ = done
	_ = time.Second
}
