package tui

import "testing"

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
