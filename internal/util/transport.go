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

func InsecureMode() bool {
	insecureOnce.Do(func() {
		v := strings.ToLower(os.Getenv("GGCODE_INSECURE"))
		insecureValue = v == "1" || v == "true" || v == "yes"
	})
	return insecureValue
}

func WrapTransport(base *http.Transport) *http.Transport {
	var t *http.Transport
	if base != nil {
		t = base.Clone()

		if t.Proxy == nil {
			t.Proxy = SmartProxyFunc()
		}
	} else {
		t = &http.Transport{
			Proxy: SmartProxyFunc(),
		}
	}

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

func NewInsecureAwareClient(timeout time.Duration) *http.Client {
	transport := WrapTransport(nil)
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
