package provider

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"
)

const defaultStreamIdleReadTimeout = 10 * time.Minute

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
		return 0
	}
	return time.Duration(secs) * time.Second
}

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
