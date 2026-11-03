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

func newProviderHTTPTransport() http.RoundTripper {
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

		ForceAttemptHTTP2: false,
	}
	t := util.WrapTransport(base)
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}

	t.TLSClientConfig.NextProtos = []string{"http/1.1"}

	timeout := streamIdleReadTimeoutFromEnv()
	if timeout > 0 {
		return &idleTimeoutTransport{base: t, timeout: timeout}
	}
	return t
}
