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

func newMCPHTTPTransport() *http.Transport {
	base := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   mcpDialTimeout,
			KeepAlive: mcpDialTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   mcpTLSHandshakeTimeout,
		ResponseHeaderTimeout: mcpResponseHeaderTimeout,
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
	}
	return util.WrapTransport(base)
}

func newMCPHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: newMCPHTTPTransport(),
	}
}
