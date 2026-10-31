package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestPlainArgsAreFlags verifies the subcommand-vs-positional switch routing:
// leading "--" selects the flag mode, everything else is the switch form.
func TestPlainArgsAreFlags(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"zai"}, false},
		{[]string{"zai", "ep1"}, false},
		{[]string{"--list"}, true},
		{[]string{"--all"}, true},
		{[]string{"--show-models"}, true},
	}
	for _, c := range cases {
		if got := plainArgsAreFlags(c.args); got != c.want {
			t.Errorf("plainArgsAreFlags(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

// newPlainListFixture builds an in-memory config with three endpoints covering
// the filter classes: keyed+modelled, keyed+no-models, no-key+modelled.
func newPlainListFixture() *config.Config {
	cfg := &config.Config{
		Vendor:   "vkeyed",
		Endpoint: "ep-both",
		Vendors: map[string]config.VendorConfig{
			"vkeyed": {Endpoints: map[string]config.EndpointConfig{
				"ep-both": {
					APIKey:       "k",
					Models:       []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9", "m10", "m11", "m12"},
					DefaultModel: "m1",
				},
			}},
			"vnomodels": {APIKey: "k", Endpoints: map[string]config.EndpointConfig{
				"ep-nomodels": {APIKey: "k"},
			}},
			"vnokey": {Endpoints: map[string]config.EndpointConfig{
				"ep-nokey": {Models: []string{"m1"}},
			}},
		},
	}
	return cfg
}

// captureStdout runs fn with os.Stdout piped and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Skipf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	_ = r.Close()
	return string(b)
}

// TestPlainListEndpointsDefaultFilter verifies the default view lists only
// endpoints with BOTH a usable api key and a configured model list, caps the
// model listing at 10 with a +N more hint, and marks the active endpoint.
func TestPlainListEndpointsDefaultFilter(t *testing.T) {
	cfg := newPlainListFixture()
	out := captureStdout(t, func() { plainListEndpoints(cfg, false) })

	if !strings.Contains(out, "vendor vkeyed") {
		t.Error("keyed+modelled vendor should be listed")
	}
	if strings.Contains(out, "vnomodels") || strings.Contains(out, "ep-nomodels") {
		t.Error("keyed-but-modelless endpoint must be hidden by default")
	}
	if strings.Contains(out, "vnokey") || strings.Contains(out, "ep-nokey") {
		t.Error("keyless endpoint must be hidden by default")
	}
	if !strings.Contains(out, "(+2 more") {
		t.Errorf("12 models should cap at 10 with '(+2 more' hint, got:\n%s", out)
	}
	if !strings.Contains(out, "* ep-both") {
		t.Errorf("active endpoint should carry the '*' marker, got:\n%s", out)
	}
	if strings.Count(out, "    - ")+strings.Count(out, "    * ") != 10 {
		t.Errorf("default view must show at most 10 models, got:\n%s", out)
	}
}

// TestPlainListEndpointsAll verifies --all surfaces the filtered-out classes
// with their annotations.
func TestPlainListEndpointsAll(t *testing.T) {
	cfg := newPlainListFixture()
	out := captureStdout(t, func() { plainListEndpoints(cfg, true) })

	for _, want := range []string{"ep-both", "ep-nomodels", "(no models configured)", "ep-nokey", "(no api key)"} {
		if !strings.Contains(out, want) {
			t.Errorf("--all output should contain %q, got:\n%s", want, out)
		}
	}
}

// TestPlainAddEndpointValidation verifies the guard rails that run BEFORE any
// config mutation: required fields, protocol whitelist, duplicate id.
func TestPlainAddEndpointValidation(t *testing.T) {
	cfg := newPlainListFixture()

	out := captureStdout(t, func() { plainAddEndpoint(cfg, "", "openai", "https://x", "m", 0, 0, "") })
	if !strings.Contains(out, "required") {
		t.Errorf("missing name must be rejected, got: %s", out)
	}

	out = captureStdout(t, func() { plainAddEndpoint(cfg, "ep9", "grpc", "https://x", "m", 0, 0, "") })
	if !strings.Contains(out, "unsupported endpoint-type") {
		t.Errorf("unknown protocol must be rejected, got: %s", out)
	}

	out = captureStdout(t, func() { plainAddEndpoint(cfg, "ep-both", "openai", "https://x", "m", 0, 0, "") })
	if !strings.Contains(out, "already exists") {
		t.Errorf("duplicate endpoint id must be rejected, got: %s", out)
	}

	if _, ok := cfg.Vendors["vkeyed"].Endpoints["ep-both"]; !ok {
		t.Fatal("fixture endpoint vanished - validation must not mutate config")
	}
}

// TestPlainAddModelValidation verifies the add-model guards: model required,
// active endpoint must exist, duplicate model refused.
func TestPlainAddModelValidation(t *testing.T) {
	cfg := newPlainListFixture()

	out := captureStdout(t, func() { plainAddModel(cfg, "", "") })
	if !strings.Contains(out, "required") {
		t.Errorf("empty model must be rejected, got: %s", out)
	}

	out = captureStdout(t, func() { plainAddModel(cfg, "m1", "") })
	if !strings.Contains(out, "already on endpoint") {
		t.Errorf("duplicate model must be rejected, got: %s", out)
	}

	// No active endpoint: refuse instead of creating one.
	cfg2 := newPlainListFixture()
	cfg2.Vendor = ""
	out = captureStdout(t, func() { plainAddModel(cfg2, "newmodel", "") })
	if !strings.Contains(out, "no active endpoint") {
		t.Errorf("missing active endpoint must be rejected, got: %s", out)
	}
}
