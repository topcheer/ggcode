package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// blockingBody simulates a dropped proxy tunnel: reads block forever and
// never return bytes (the connection died without FIN/RST).
type blockingBody struct {
	closed chan struct{}
}

func newBlockingBody() *blockingBody { return &blockingBody{closed: make(chan struct{})} }

func (b *blockingBody) Read(p []byte) (int, error) {
	<-b.closed // block forever until closed
	return 0, io.EOF
}

func (b *blockingBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

// stubRoundTripper returns a canned response whose body is injected.
type stubRoundTripper struct {
	status    int
	body      io.ReadCloser
	gotCtx    context.Context
	closeSeen bool
}

func (s *stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.gotCtx = req.Context()
	return &http.Response{
		StatusCode: s.status,
		Header:     make(http.Header),
		Body:       s.body,
		Request:    req,
	}, nil
}

func TestIdleTimeoutTransportCutsSilentBody(t *testing.T) {
	// The body mimics real net/http semantics: a Read blocked on a dead
	// connection returns once the request context is cancelled (transport
	// tears down the conn). This is the guarantee idleTimeoutTransport
	// relies on to unblock the SSE read loop.
	stub := &ctxBodyTransport{}
	tr := &idleTimeoutTransport{base: stub, timeout: 80 * time.Millisecond}

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "http://example.test/v1", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, rerr := resp.Body.Read(make([]byte, 16))
		errCh <- rerr
	}()

	select {
	case rerr := <-errCh:
		if rerr == nil {
			t.Fatal("expected error from blocked Read after idle timeout, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read still blocked after idle deadline - watchdog did not cancel")
	}

	select {
	case <-stub.gotCtx.Done():
	default:
		t.Fatal("request context was not cancelled by the watchdog")
	}
}

// ctxBodyTransport returns a response whose body blocks until the request
// context dies - the net/http behavior for a wedged stream connection.
type ctxBodyTransport struct{ gotCtx context.Context }

func (s *ctxBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.gotCtx = req.Context()
	return &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       ctxBody{ctx: req.Context()},
		Request:    req,
	}, nil
}

type ctxBody struct{ ctx context.Context }

func (b ctxBody) Read(p []byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b ctxBody) Close() error { return nil }

func TestIdleTimeoutBodyResetsOnData(t *testing.T) {
	// Feed data slowly: each chunk arrives well within the idle window, so
	// the watchdog keeps resetting and the stream completes normally.
	slow := io.NopCloser(io.MultiReader(
		bytes.NewReader([]byte("data: chunk1\n\n")),
		&delayedReader{d: 150 * time.Millisecond, r: bytes.NewReader([]byte("data: chunk2\n\n"))},
	))
	stub := &stubRoundTripper{status: 200, body: slow}
	tr := &idleTimeoutTransport{base: stub, timeout: 300 * time.Millisecond}

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "http://example.test/v1", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	all, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		t.Fatalf("ReadAll: %v (idle watchdog fired despite live data)", rerr)
	}
	if !bytes.Contains(all, []byte("chunk2")) {
		t.Fatalf("missing second chunk: %q", all)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestIdleTimeoutTransportCutsBlockedRoundTrip(t *testing.T) {
	// Simulates the request-write wedge from the proxy-hang postmortem:
	// RoundTrip never returns (write to a dead peer / waiting for response
	// headers on a CLOSE-WAIT conn). net/http aborts such a request once
	// its context is cancelled, which is what the stub models.
	stub := &ctxBlockedRoundTripper{}
	tr := &idleTimeoutTransport{base: stub, timeout: 80 * time.Millisecond}

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "http://example.test/v1", nil)

	errCh := make(chan error, 1)
	go func() {
		_, err := tr.RoundTrip(req)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from blocked RoundTrip after idle timeout, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RoundTrip still blocked - watchdog did not cover the write/headers phase")
	}
}

// ctxBlockedRoundTripper blocks inside RoundTrip until the request context
// is cancelled, mirroring net/http's abort-on-ctx-done behavior for wedged
// request writes.
type ctxBlockedRoundTripper struct{}

func (s *ctxBlockedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestIdleTimeoutTransportDisabled(t *testing.T) {
	// timeout <= 0 must bypass the wrapper entirely.
	stub := &stubRoundTripper{status: 200, body: newBlockingBody()}
	tr := &idleTimeoutTransport{base: stub, timeout: 0}

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "http://example.test/v1", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if _, isWrapped := resp.Body.(*idleTimeoutBody); isWrapped {
		t.Fatal("body should not be wrapped when timeout is disabled")
	}
}

// delayedReader pauses before delegating the first Read, simulating an SSE
// keep-alive gap shorter than the idle window.
type delayedReader struct {
	d      time.Duration
	r      io.Reader
	waited bool
}

func (d *delayedReader) Read(p []byte) (int, error) {
	if !d.waited {
		time.Sleep(d.d)
		d.waited = true
	}
	return d.r.Read(p)
}

func TestStreamIdleReadTimeoutFromEnv(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("GGCODE_STREAM_IDLE_TIMEOUT", "")
		if got := streamIdleReadTimeoutFromEnv(); got != defaultStreamIdleReadTimeout {
			t.Fatalf("default: got %v", got)
		}
	})
	t.Run("override", func(t *testing.T) {
		t.Setenv("GGCODE_STREAM_IDLE_TIMEOUT", "42")
		if got := streamIdleReadTimeoutFromEnv(); got != 42*time.Second {
			t.Fatalf("override: got %v", got)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		t.Setenv("GGCODE_STREAM_IDLE_TIMEOUT", "0")
		if got := streamIdleReadTimeoutFromEnv(); got != 0 {
			t.Fatalf("disabled: got %v", got)
		}
	})
	t.Run("garbage falls back to default", func(t *testing.T) {
		t.Setenv("GGCODE_STREAM_IDLE_TIMEOUT", "not-a-number")
		if got := streamIdleReadTimeoutFromEnv(); got != defaultStreamIdleReadTimeout {
			t.Fatalf("garbage: got %v", got)
		}
	})
}
