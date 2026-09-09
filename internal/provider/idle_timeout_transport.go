package provider

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"
)

// streamIdleReadTimeout bounds how long a response body may stay silent
// (no bytes read) before the underlying request context is cancelled and the
// blocked Read returns. Without it, a dropped proxy connection (proxy accepts
// the TCP peer, response headers already flushed, then the tunnel dies
// without FIN/RST) leaves the SSE read loop blocked forever: net/http has no
// idle/read deadline for body bytes, http.Client.Timeout is deliberately 0
// for streaming LLM responses, and the agent-layer stall detector is advisory
// only (it warns, never cancels).
//
// 10 minutes is deliberately generous: during long reasoning pauses providers
// send SSE heartbeats (Anthropic ping events, OpenAI keep-alive comments),
// but some proxies strip them, so the threshold must tolerate a genuinely
// quiet - yet alive - stream. A dead tunnel produces no bytes ever, so it is
// still cut off eventually instead of hanging forever.
const defaultStreamIdleReadTimeout = 10 * time.Minute

// streamIdleReadTimeoutFromEnv allows overriding the idle-read window via
// GGCODE_STREAM_IDLE_TIMEOUT (seconds). "0" disables the guard entirely.
func streamIdleReadTimeoutFromEnv() time.Duration {
	v := os.Getenv("GGCODE_STREAM_IDLE_TIMEOUT")
	if v == "" {
		return defaultStreamIdleReadTimeout
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return defaultStreamIdleReadTimeout
	}
	if secs == 0 {
		return 0 // disabled
	}
	return time.Duration(secs) * time.Second
}

// idleTimeoutTransport wraps a RoundTripper and cancels the request context
// once the response body stays unreadably silent for the configured window.
// Cancelling the context is the only portable way to unblock a body Read:
// it tears down the underlying connection for both HTTP/1.1 and HTTP/2, and
// does not require reaching the net.Conn (unreachable through the SDK's
// response body wrappers).
type idleTimeoutTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

func (t *idleTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.timeout <= 0 {
		return t.base.RoundTrip(req)
	}
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	// Start the watchdog BEFORE the request is written, not after headers
	// arrive: a dropped proxy can also wedge the request-write phase (peer
	// dead with a full send buffer / zero window — writes never complete,
	// CLOSE-WAIT with unread recv-Q data, dirty reused idle conn), a phase
	// where ResponseHeaderTimeout has not started counting yet. One idle
	// window uniformly covers write + headers + body.
	timer := time.AfterFunc(t.timeout, cancel)

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}

	resp.Body = &idleTimeoutBody{
		rc:      resp.Body,
		cancel:  cancel,
		timer:   timer,
		timeout: t.timeout,
	}
	return resp, nil
}

// idleTimeoutBody resets the idle deadline on every successful read and
// stops the watchdog on Close. Read errors (including the watchdog's
// context.Canceled) are passed through unchanged.
type idleTimeoutBody struct {
	rc interface {
		Read(p []byte) (int, error)
		Close() error
	}
	cancel  context.CancelFunc
	timer   *time.Timer
	timeout time.Duration
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if err == nil && n > 0 {
		b.timer.Reset(b.timeout)
	}
	if err != nil {
		b.timer.Stop()
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.timer.Stop()
	b.cancel()
	return b.rc.Close()
}
