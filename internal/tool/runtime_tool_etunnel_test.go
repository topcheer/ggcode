package tool

import (
	"strings"
	"testing"
)

// etRuntimeProvider is a minimal fake satisfying RuntimeStatusProvider.
type etRuntimeProvider struct{}

func (etRuntimeProvider) RuntimeSessionID() string                  { return "s-1" }
func (etRuntimeProvider) RuntimePermissionMode() string             { return "bypass" }
func (etRuntimeProvider) RuntimeVendor() string                     { return "zai" }
func (etRuntimeProvider) RuntimeEndpoint() string                   { return "ep" }
func (etRuntimeProvider) RuntimeModel() string                      { return "m" }
func (etRuntimeProvider) RuntimeLanguage() string                   { return "en" }
func (etRuntimeProvider) RuntimeContextWindow() int                 { return 0 }
func (etRuntimeProvider) RuntimeMaxTokens() int                     { return 0 }
func (etRuntimeProvider) RuntimeIMAdapters() []RuntimeIMAdapterInfo { return nil }
func (etRuntimeProvider) RuntimeMobile() RuntimeMobileInfo          { return RuntimeMobileInfo{} }
func (etRuntimeProvider) RuntimeEtunnel() RuntimeEtunnelInfo        { return RuntimeEtunnelInfo{} }

type etActiveProvider struct {
	etRuntimeProvider
	et RuntimeEtunnelInfo
}

func (p etActiveProvider) RuntimeEtunnel() RuntimeEtunnelInfo { return p.et }

func TestRuntimeEtunnelActive(t *testing.T) {
	p := etActiveProvider{et: RuntimeEtunnelInfo{Active: true, URL: "https://demo.t.ggcode.uk", Subdomain: "demo"}}
	out, err := (RuntimeTool{Provider: p}).Execute(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Etunnel: active", "URL: https://demo.t.ggcode.uk", "Subdomain: demo"} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("runtime output missing %q; got:\n%s", want, out.Content)
		}
	}
}

func TestRuntimeEtunnelInactive(t *testing.T) {
	out, err := (RuntimeTool{Provider: etRuntimeProvider{}}).Execute(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Content, "Etunnel") {
		t.Errorf("inactive etunnel must not render an Etunnel section; got:\n%s", out.Content)
	}
}
