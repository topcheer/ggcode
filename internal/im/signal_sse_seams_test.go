package im

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Pins for the r194 sseLoop decomposition (signal receive loop). The seams are
// behavior-preserving extractions from the former inline loop body; these
// tests pin the extracted semantics without waiting out the real 5s backoff.

// ---- backoff seam pins -----------------------------------------------------

func TestSignalReceiveBackoffConstant(t *testing.T) {
	if signalReceiveBackoff != 5*time.Second {
		t.Fatalf("signalReceiveBackoff = %v, want 5s (historical receive-loop backoff)", signalReceiveBackoff)
	}
}

func TestSSEBackoffWaitImmediateCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if sseBackoffWait(ctx, time.Hour) {
		t.Fatal("sseBackoffWait(ctx done, 1h) = true, want false")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("sseBackoffWait blocked %v on cancelled ctx, want immediate return", elapsed)
	}
}

func TestSSEBackoffWaitCancelDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	if sseBackoffWait(ctx, 10*time.Second) {
		t.Fatal("sseBackoffWait = true after ctx cancel during wait, want false")
	}
	if elapsed := time.Since(start); elapsed >= signalReceiveBackoff {
		t.Fatalf("sseBackoffWait waited %v after cancel, want return before full backoff", elapsed)
	}
}

func TestSSEBackoffWaitElapsed(t *testing.T) {
	start := time.Now()
	if !sseBackoffWait(context.Background(), 20*time.Millisecond) {
		t.Fatal("sseBackoffWait(20ms, live ctx) = false, want true")
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("sseBackoffWait returned after %v, want >= 20ms", elapsed)
	}
}

func TestSSEBackoffDelegatesFixedDuration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sseBackoff(ctx) {
		t.Fatal("sseBackoff(cancelled ctx) = true, want false")
	}
}

// ---- receivePollOnce routing pins -----------------------------------------

type sseStubRoundTripper struct {
	resp *http.Response
	err  error
}

func (s *sseStubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return s.resp, s.err
}

type sseStubReadCloser struct {
	content string
	pos     int
	closed  *bool
	readErr error
}

func (s *sseStubReadCloser) Read(p []byte) (int, error) {
	if s.readErr != nil {
		return 0, s.readErr
	}
	if s.pos >= len(s.content) {
		return 0, io.EOF
	}
	n := copy(p, s.content[s.pos:])
	s.pos += n
	return n, nil
}

func (s *sseStubReadCloser) Close() error {
	if s.closed != nil {
		*s.closed = true
	}
	return nil
}

func TestReceivePollOnceRouting(t *testing.T) {
	newAdapter := func() *signalAdapter {
		return &signalAdapter{name: "t", baseURL: "http://127.0.0.1:1", account: "+15550001111"}
	}
	okBody := `[{"foo":1}]`
	cancelledCtx := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}

	cases := []struct {
		desc        string
		ctx         context.Context
		url         string
		rt          *sseStubRoundTripper
		wantOutcome ssePollOutcome
		wantBody    string
	}{
		{
			desc: "request build error with live ctx retries",
			ctx:  context.Background(),
			// 0x7f control char in URL → http.NewRequestWithContext error.
			url:         "http://127.0.0.1:1/\x7fbad",
			wantOutcome: ssePollRetry,
		},
		{
			desc:        "transport error with live ctx retries",
			ctx:         context.Background(),
			rt:          &sseStubRoundTripper{err: errors.New("dial fail")},
			wantOutcome: ssePollRetry,
		},
		{
			desc:        "transport error with cancelled ctx stops",
			ctx:         cancelledCtx(),
			rt:          &sseStubRoundTripper{err: errors.New("dial fail")},
			wantOutcome: ssePollStop,
		},
		{
			desc: "body read error retries",
			ctx:  context.Background(),
			rt: &sseStubRoundTripper{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       &sseStubReadCloser{readErr: io.ErrUnexpectedEOF},
				Header:     http.Header{},
			}},
			wantOutcome: ssePollRetry,
		},
		{
			desc: "non-200/204 status retries",
			ctx:  context.Background(),
			rt: &sseStubRoundTripper{resp: &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       &sseStubReadCloser{content: "oops"},
				Header:     http.Header{},
			}},
			wantOutcome: ssePollRetry,
		},
		{
			desc: "302 redirect status retries",
			ctx:  context.Background(),
			rt: &sseStubRoundTripper{resp: &http.Response{
				StatusCode: http.StatusFound,
				Body:       &sseStubReadCloser{},
				Header:     http.Header{},
			}},
			wantOutcome: ssePollRetry,
		},
		{
			desc: "200 yields body",
			ctx:  context.Background(),
			rt: &sseStubRoundTripper{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       &sseStubReadCloser{content: okBody},
				Header:     http.Header{},
			}},
			wantOutcome: ssePollOK,
			wantBody:    okBody,
		},
		{
			desc: "204 yields body",
			ctx:  context.Background(),
			rt: &sseStubRoundTripper{resp: &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       &sseStubReadCloser{},
				Header:     http.Header{},
			}},
			wantOutcome: ssePollOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			a := newAdapter()
			client := &http.Client{Transport: tc.rt}
			url := tc.url
			if url == "" {
				url = a.baseURL + "/v1/receive/" + "%2B15550001111"
			}
			body, outcome := a.receivePollOnce(tc.ctx, client, url)
			if outcome != tc.wantOutcome {
				t.Fatalf("receivePollOnce outcome = %v, want %v", outcome, tc.wantOutcome)
			}
			if tc.wantOutcome == ssePollOK && string(body) != tc.wantBody {
				t.Fatalf("receivePollOnce body = %q, want %q", string(body), tc.wantBody)
			}
		})
	}
}

func TestReceivePollOnceClosesResponseBody(t *testing.T) {
	a := &signalAdapter{name: "t", baseURL: "http://127.0.0.1:1", account: "+15550001111"}
	closed := false
	rt := &sseStubRoundTripper{resp: &http.Response{
		StatusCode: http.StatusOK,
		Body:       &sseStubReadCloser{content: "[]", closed: &closed},
		Header:     http.Header{},
	}}
	client := &http.Client{Transport: rt}
	if _, outcome := a.receivePollOnce(context.Background(), client, a.baseURL+"/v1/receive/x"); outcome != ssePollOK {
		t.Fatalf("outcome = %v, want ssePollOK", outcome)
	}
	if !closed {
		t.Fatal("receivePollOnce did not close the response body")
	}
}

// ---- parseReceiveEnvelopes pins -------------------------------------------

func TestParseReceiveEnvelopesSeams(t *testing.T) {
	a := &signalAdapter{name: "t"}

	envs, ok := a.parseReceiveEnvelopes([]byte(`[{"a":1}]`))
	if !ok || len(envs) != 1 {
		t.Fatalf("array body: ok=%v len=%d, want ok with 1 envelope", ok, len(envs))
	}

	envs, ok = a.parseReceiveEnvelopes([]byte(`[]`))
	if !ok || len(envs) != 0 {
		t.Fatalf("empty array body: ok=%v len=%d, want ok with 0 envelopes", ok, len(envs))
	}

	envs, ok = a.parseReceiveEnvelopes([]byte(`null`))
	if !ok || envs != nil {
		t.Fatalf("null body: ok=%v envs=%v, want ok with nil slice", ok, envs)
	}

	if _, ok = a.parseReceiveEnvelopes([]byte(`<html>gateway error</html>`)); ok {
		t.Fatal("HTML body must be a retryable parse failure (#432/#968)")
	}

	if _, ok = a.parseReceiveEnvelopes([]byte(`{"a":1}`)); ok {
		t.Fatal("non-array JSON must be a retryable parse failure")
	}
}

// ---- orchestrator pins ------------------------------------------------------

func TestSSELoopCancelledContextReturnsNil(t *testing.T) {
	a := &signalAdapter{name: "t", baseURL: "http://127.0.0.1:1", account: "+15550001111"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- a.sseLoop(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sseLoop(cancelled ctx) = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sseLoop did not stop on cancelled ctx")
	}
}

func TestSSELoopPollsReceiveEndpointUntilCtxDone(t *testing.T) {
	var polls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&polls, 1)
		if r.URL.Path != "/v1/receive/+15550001111" {
			t.Errorf("poll path = %q, want /v1/receive/+15550001111", r.URL.Path)
		}
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	a := &signalAdapter{name: "t", baseURL: srv.URL, account: "+15550001111"}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := a.sseLoop(ctx); err != nil {
		t.Fatalf("sseLoop = %v, want nil", err)
	}
	if atomic.LoadInt32(&polls) == 0 {
		t.Fatal("sseLoop never polled the receive endpoint")
	}
}
