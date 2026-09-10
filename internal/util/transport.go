package util

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	insecureOnce  sync.Once
	insecureValue bool
)

// InsecureMode returns true when the GGCODE_INSECURE environment variable is
// set to a truthy value ("1", "true", "yes").  When true, all outbound HTTP
// transports created through WrapTransport or NewInsecureAwareClient will
// skip TLS certificate verification.
//
// The result is cached after the first call so the env var is only read once.
func InsecureMode() bool {
	insecureOnce.Do(func() {
		v := strings.ToLower(os.Getenv("GGCODE_INSECURE"))
		insecureValue = v == "1" || v == "true" || v == "yes"
	})
	return insecureValue
}

// WrapTransport returns a new *http.Transport that clones the base transport
// and, when GGCODE_INSECURE is active, sets TLSClientConfig.InsecureSkipVerify.
// If base is nil, a fresh http.Transport is created.
func WrapTransport(base *http.Transport) *http.Transport {
	var t *http.Transport
	if base != nil {
		t = base.Clone()
		// #1849 case 1: Clone preserves the base's Proxy verbatim - callers
		// like a2a WithMTLS build a transport for its TLS fields only and
		// leave Proxy nil, which silently meant DIRECT connection: the env
		// proxy (ProxyFromEnvironment) and the Windows sysproxy chain both
		// stopped working behind corporate proxies. Only honor an explicit
		// caller-set Proxy; a nil Proxy gets the smart default.
		if t.Proxy == nil {
			t.Proxy = SmartProxyFunc()
		}
	} else {
		t = &http.Transport{
			Proxy: SmartProxyFunc(),
		}
	}
	// #1849 case 3: staged timeouts for the fresh-transport paths - the
	// a2a mTLS client runs with Client.Timeout == 0 on purpose (#1458-B),
	// so without per-stage defaults a hung dial or silent peer could pin
	// the connection for as long as the (possibly nil) context allows.
	if t.DialContext == nil {
		t.DialContext = (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext
	}
	if t.TLSHandshakeTimeout == 0 {
		t.TLSHandshakeTimeout = 15 * time.Second
	}
	if t.ResponseHeaderTimeout == 0 {
		t.ResponseHeaderTimeout = 10 * time.Minute
	}
	// Disable HTTP/2 globally to prevent a crash in net/http's http2 client
	// conn readLoop on Windows (Go 1.26.x). The http2Framer.ReadFrameHeader
	// panics with a nil pointer when the peer resets the connection during
	// TLS handshake. HTTP/1.1 is universally supported.
	t.ForceAttemptHTTP2 = false
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}
	t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	if InsecureMode() {
		t.TLSClientConfig.InsecureSkipVerify = true
	}
	return t
}

// NewInsecureAwareClient returns an *http.Client with the given timeout.
// If GGCODE_INSECURE is set, the client's transport will skip TLS verification.
func NewInsecureAwareClient(timeout time.Duration) *http.Client {
	transport := WrapTransport(nil)
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
