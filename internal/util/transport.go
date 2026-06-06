package util

import (
	"crypto/tls"
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
	} else {
		t = &http.Transport{}
	}
	if InsecureMode() {
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{}
		}
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
