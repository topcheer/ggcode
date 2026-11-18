package config

import "testing"

// #5779: tombstone paths for endpoint names containing dots (e.g.
// "github.com") must survive the save pipeline without being mis-split.
func Test5779DottedEndpointTombstone(t *testing.T) {
	existing := map[string]interface{}{
		"vendors": map[string]interface{}{
			"acme": map[string]interface{}{
				"endpoints": map[string]interface{}{
					"github.com": map[string]interface{}{
						"context_window": float64(0), // zero → tombstone expected
						"max_tokens":     float64(4096),
					},
				},
			},
		},
	}

	zeroPaths := endpointZeroClearPaths(tombedConfigFor5779())
	if len(zeroPaths) != 1 {
		t.Fatalf("expected 1 zero path, got %d: %v", len(zeroPaths), zeroPaths)
	}
	path := zeroPaths[0]
	if len(path) != 5 || path[3] != "github.com" {
		t.Fatalf("dotted endpoint name must stay one segment, got %v", path)
	}
	_ = existing
}

func tombedConfigFor5779() *Config {
	c := &Config{Vendors: map[string]VendorConfig{
		"acme": {Endpoints: map[string]EndpointConfig{
			"github.com": {ContextWindow: 0, MaxTokens: 4096},
		}},
	}}
	return c
}

func Test5779YamlPathLookupDottedKey(t *testing.T) {
	raw := map[string]interface{}{
		"vendors": map[string]interface{}{
			"acme": map[string]interface{}{
				"endpoints": map[string]interface{}{
					"github.com": map[string]interface{}{"context_window": float64(0)},
				},
			},
		},
	}
	path := []string{"vendors", "acme", "endpoints", "github.com", "context_window"}
	if _, ok := yamlPathLookup(raw, path); !ok {
		t.Fatal("dotted endpoint key must be reachable via []string path")
	}
	yamlPathDelete(raw, path)
	if _, ok := yamlPathLookup(raw, path); ok {
		t.Fatal("dotted endpoint key must be deleted in one pass")
	}
}
