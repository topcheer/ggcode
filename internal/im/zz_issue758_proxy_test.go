package im

import (
	"net/http"
	"net/url"
	"testing"
)

// Guards for #758: the per-host proxy interceptor replaces the old
// HTTPS_PROXY env hack (net/http sync.Once caching made it either inert or
// permanently polluting).
func TestHostProxyInterceptor(t *testing.T) {
	const host = "relay.example.test:443"
	const proxy = "socks5://127.0.0.1:1080"

	if err := RegisterHostProxy(host, proxy); err != nil {
		t.Fatalf("register: %v", err)
	}
	defer UnregisterHostProxy(host)

	// Registered host resolves to the pinned proxy.
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: host}}
	got, hit := pinnedProxyFor(req)
	if !hit || got == nil || got.String() != proxy {
		t.Fatalf("registered host must use pinned proxy, got %v hit=%v", got, hit)
	}

	// Unregistered host must NOT get the pinned proxy.
	other := &http.Request{URL: &url.URL{Scheme: "https", Host: "other.example.test:443"}}
	got2, _ := pinnedProxyFor(other)
	if got2 != nil && got2.String() == proxy {
		t.Fatal("unregistered host must not be routed through the pinned proxy")
	}

	// Unregister removes the route.
	UnregisterHostProxy(host)
	got3, _ := pinnedProxyFor(req)
	if got3 != nil && got3.String() == proxy {
		t.Fatal("unregistered host must lose its pinned proxy")
	}

	// Invalid proxy URL surfaces as an error instead of silent misrouting.
	if err := RegisterHostProxy("bad.test:443", "://invalid"); err == nil {
		UnregisterHostProxy("bad.test:443")
		t.Fatal("invalid proxy URL must return an error")
	}
}
