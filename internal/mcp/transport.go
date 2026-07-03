package mcp

import (
	"net"
	"net/http"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

const (
	mcpDialTimeout           = 30 * time.Second
	mcpTLSHandshakeTimeout   = 10 * time.Second
	mcpResponseHeaderTimeout = 120 * time.Second
)

// newMCPHTTPTransport creates an *http.Transport with proxy support
// (from environment variables), transport-level timeouts, and optional
// insecure TLS (when GGCODE_INSECURE is set).
//
// This preserves both the timeout settings that existed before the proxy
// integration and the proxy/insecure support added afterward.
func newMCPHTTPTransport() *http.Transport {
	base := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   mcpDialTimeout,
			KeepAlive: mcpDialTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   mcpTLSHandshakeTimeout,
		ResponseHeaderTimeout: mcpResponseHeaderTimeout,
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
	}
	return util.WrapTransport(base)
}

// newMCPHTTPClient returns an *http.Client using newMCPHTTPTransport.
// Pass timeout=0 to rely on per-request context cancellation (used by the
// MCP client for request/response, since individual requests carry their
// own deadlines via context).
func newMCPHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: newMCPHTTPTransport(),
	}
}
