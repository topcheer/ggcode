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
	providerResponseHeaderTimeout = 5 * time.Minute
)

func newProviderHTTPTransport() *http.Transport {
	base := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   providerDialTimeout,
			KeepAlive: providerDialTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   providerTLSHandshakeTimeout,
		ResponseHeaderTimeout: providerResponseHeaderTimeout,
	}
	return util.WrapTransport(base)
}
