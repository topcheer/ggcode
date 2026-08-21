package provider

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

const (
	providerDialTimeout           = 30 * time.Second
	providerTLSHandshakeTimeout   = 10 * time.Second
	providerResponseHeaderTimeout = 120 * time.Second
	providerIdleConnTimeout       = 90 * time.Second
	providerMaxIdleConns          = 20
	providerMaxIdleConnsPerHost   = 5
)

func newProviderHTTPTransport() *http.Transport {
	base := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   providerDialTimeout,
			KeepAlive: providerDialTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   providerTLSHandshakeTimeout,
		ResponseHeaderTimeout: providerResponseHeaderTimeout,
		IdleConnTimeout:       providerIdleConnTimeout,
		MaxIdleConns:          providerMaxIdleConns,
		MaxIdleConnsPerHost:   providerMaxIdleConnsPerHost,
		Proxy:                 util.SmartProxyFunc(),
		// Disable HTTP/2 to prevent a crash in net/http's http2 client conn
		// readLoop on Windows (Go 1.26.x). The http2Framer.ReadFrameHeader
		// panics with a nil pointer when the connection is reset by the peer
		// during the TLS handshake. HTTP/1.1 is universally supported by all
		// LLM API endpoints and avoids this Go runtime bug entirely.
		ForceAttemptHTTP2: false,
	}
	t := util.WrapTransport(base)
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}
	// Restrict ALPN to HTTP/1.1 only — prevents the server from upgrading
	// to HTTP/2 even if it advertises it.
	t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return t
}
