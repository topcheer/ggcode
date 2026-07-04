package provider

import (
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
		Proxy:                 http.ProxyFromEnvironment,
	}
	return util.WrapTransport(base)
}
