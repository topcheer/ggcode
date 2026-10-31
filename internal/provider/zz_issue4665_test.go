package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #4665: the non-gateway discovery cache key must distinguish same-host
// upstreams. The old key was bare Hostname() - no port, no path - so
// model lists cross-contaminated for the 6h (disk-persisted) TTL:
//   - same host, different PORT (localhost:11434 vs localhost:1234);
//   - same host:port, different base PATH (reverse proxy splitting
//     gw.example.com/oa -> A and /gw -> B).
// Sharing stays intentional for the oneapi/litellm form: same host:port
// AND same base path = one registry (asserted by the updated
// TestDiscoverModelsReusesSameHostCacheForNonGatewayVendor).

func issue4665CountingServer(hits *atomic.Int32, models string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(models))
	}))
}

// issue4665Port extracts the port an httptest server listens on, so tests
// can build same-host different-path URLs against two distinct servers.
func issue4665Port(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	return u.Port()
}

func TestIssue4665DifferentPortSameHostDoesNotShare(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
	var hitsA, hitsB atomic.Int32
	srvA := issue4665CountingServer(&hitsA, `{"data":[{"id":"ollama-model"}]}`)
	defer srvA.Close()
	srvB := issue4665CountingServer(&hitsB, `{"data":[{"id":"lmstudio-model"}]}`)
	defer srvB.Close()

	_, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID: "custom", EndpointID: "oa", Protocol: "openai",
		BaseURL: srvA.URL + "/v1", APIKey: "k",
	})
	if err != nil {
		t.Fatalf("discover A: %v", err)
	}
	b, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID: "custom", EndpointID: "lm", Protocol: "openai",
		BaseURL: srvB.URL + "/v1", APIKey: "k",
	})
	if err != nil {
		t.Fatalf("discover B: %v", err)
	}
	// httptest servers share host 127.0.0.1 but bind different ports.
	if hitsA.Load() != 1 || hitsB.Load() != 1 {
		t.Fatalf("hits A=%d B=%d, want 1/1 (different ports must not share cache)", hitsA.Load(), hitsB.Load())
	}
	if len(b) != 1 || b[0] != "lmstudio-model" {
		t.Fatalf("endpoint B got %v, want its own [lmstudio-model] (not A's list)", b)
	}
}

func TestIssue4665DifferentPathSameHostPortDoesNotShare(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
	var hitsA, hitsB atomic.Int32
	srvA := issue4665CountingServer(&hitsA, `{"data":[{"id":"upstream-a-model"}]}`)
	defer srvA.Close()
	srvB := issue4665CountingServer(&hitsB, `{"data":[{"id":"upstream-b-model"}]}`)
	defer srvB.Close()

	// Reverse-proxy split form: one host, path routes to different upstreams.
	_, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID: "custom", EndpointID: "oa", Protocol: "openai",
		BaseURL: "http://127.0.0.1:" + issue4665Port(t, srvA) + "/oa", APIKey: "k",
	})
	if err != nil {
		t.Fatalf("discover /oa: %v", err)
	}
	b, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID: "custom", EndpointID: "gw", Protocol: "openai",
		BaseURL: "http://127.0.0.1:" + issue4665Port(t, srvB) + "/gw", APIKey: "k",
	})
	if err != nil {
		t.Fatalf("discover /gw: %v", err)
	}
	if hitsA.Load() != 1 || hitsB.Load() != 1 {
		t.Fatalf("hits A=%d B=%d, want 1/1 (different paths must not share cache)", hitsA.Load(), hitsB.Load())
	}
	if len(b) != 1 || b[0] != "upstream-b-model" {
		t.Fatalf("path-split endpoint got %v, want its own [upstream-b-model]", b)
	}
}

func TestIssue4665SameHostPortPathSharesAcrossProtocols(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
	var hits atomic.Int32
	srv := issue4665CountingServer(&hits, `{"data":[{"id":"oneapi-shared"}]}`)
	defer srv.Close()

	first, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID: "custom", EndpointID: "openai", Protocol: "openai",
		BaseURL: srv.URL + "/v1", APIKey: "k",
	})
	if err != nil {
		t.Fatalf("discover openai: %v", err)
	}
	second, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID: "custom", EndpointID: "anthropic", Protocol: "anthropic",
		BaseURL: srv.URL + "/v1", APIKey: "k",
	})
	if err != nil {
		t.Fatalf("discover anthropic: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1 (oneapi form: same host:port+path shares across protocols)", hits.Load())
	}
	if len(second) != 1 || second[0] != "oneapi-shared" {
		t.Fatalf("shared registry read = %v", second)
	}
	_ = first
}

func TestIssue4665CacheKeyComponents(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		same bool
	}{
		{"same url", "https://gw.io/v1", "https://gw.io/v1", true},
		{"trailing slash", "https://gw.io/v1/", "https://gw.io/v1", true},
		{"case-insensitive host", "https://GW.io/v1", "https://gw.io/v1", true},
		{"different path", "https://gw.io/oa", "https://gw.io/gw", false},
		{"different port", "https://gw.io:8080/v1", "https://gw.io:9090/v1", false},
		{"explicit vs default port", "https://gw.io:443/v1", "https://gw.io/v1", true},
		{"path case-sensitive", "https://gw.io/OA", "https://gw.io/oa", false},
	}
	for _, c := range cases {
		ka := modelDiscoveryCacheKey(&config.ResolvedEndpoint{VendorID: "custom", BaseURL: c.a})
		kb := modelDiscoveryCacheKey(&config.ResolvedEndpoint{VendorID: "custom", BaseURL: c.b})
		if (ka == kb) != c.same {
			t.Errorf("%s: keys %q vs %q, same=%v want %v", c.name, ka, kb, ka == kb, c.same)
		}
	}
}
