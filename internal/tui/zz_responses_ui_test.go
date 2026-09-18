package tui

import "testing"

// The new-vendor/new-endpoint protocol cycler must expose every protocol the
// provider registry supports. "openai-responses" was missing, leaving the
// entire Responses stack (sa-40 adapter + server tools + verbosity + encrypted
// reasoning round-trip) reachable only by hand-editing ggcode.yaml.
func TestNewVendorProtocolsExposeResponses(t *testing.T) {
	found := false
	for _, p := range newVendorProtocols {
		if p == "openai-responses" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("newVendorProtocols = %v; want openai-responses present (provider registry supports it)", newVendorProtocols)
	}
}
