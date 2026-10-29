package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// zz_issue4266_test.go - before #4266, readResponsesStream used bare
// `ch <- StreamEvent{...}` sends: with a full channel buffer and a
// cancelled consumer ctx, the pump goroutine blocked forever (leak).
// Every send must honor ctx.Done and the pump must return promptly.

func TestReadResponsesStream_SendHonorsCtx_Issue4266(t *testing.T) {
	p := &OpenAIResponsesProvider{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: StreamEventText, Text: "fill"} // buffer now full
	cancel()                                               // consumer gone

	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"y\"}\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(sse)), Header: http.Header{}}

	done := make(chan struct{})
	var retryable bool
	var lastErr error
	go func() {
		retryable, lastErr = p.readResponsesStream(ctx, resp, ch)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("readResponsesStream hung on a bare send with full buffer + cancelled ctx (goroutine leak, #4266 regression)")
	}
	if retryable {
		t.Fatal("must not report retryable after ctx cancellation")
	}
	if lastErr != context.Canceled {
		t.Fatalf("lastErr = %v, want context.Canceled", lastErr)
	}
}
