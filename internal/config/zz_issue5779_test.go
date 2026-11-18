package config

import (
	"strings"
	"testing"
)

// Regression pin for #5779: tombstone paths built from YAML map keys must be
// bijective even when key segments contain dots (endpoint name "github.com").
// The pre-fix "." separator split [vendors github-copilot endpoints github
// com context_window] and silently skipped the clear, resurrecting cleared
// numeric limits on the next Load in the both-exist state.

func TestIssue5779ZeroClearDotEndpointName(t *testing.T) {
	cfg := &Config{Vendors: map[string]VendorConfig{
		"github-copilot": {Endpoints: map[string]EndpointConfig{
			"github.com": {}, // ContextWindow == 0, MaxTokens == 0 -> both must be cleared
			"enterprise": {},
		}},
	}}
	zeros := endpointZeroClearPaths(cfg)
	if len(zeros) != 4 {
		t.Fatalf("expected 4 zero-clear paths, got %d: %v", len(zeros), zeros)
	}

	existingRaw := map[string]interface{}{
		"vendors": map[string]interface{}{
			"github-copilot": map[string]interface{}{
				"endpoints": map[string]interface{}{
					"github.com": map[string]interface{}{
						"context_window": 123456,
						"max_tokens":     8192,
					},
					"enterprise": map[string]interface{}{
						"context_window": 123456,
						"max_tokens":     8192,
					},
				},
			},
		},
	}

	applyZeroNumberClears(existingRaw, zeros)

	// yamlPathDelete prunes empty parents upward: fully-cleared endpoints (and
	// then the vendor/vendors maps) disappear entirely. That is the correct
	// tombstone outcome - the pre-fix bug left stale fields behind. Walk with
	// comma-ok so a fully-pruned tree counts as success.
	epsAny, ok := existingRaw["vendors"]
	if ok {
		if vAny, ok2 := epsAny.(map[string]interface{})["github-copilot"]; ok2 {
			if eAny, ok3 := vAny.(map[string]interface{})["endpoints"]; ok3 {
				eps := eAny.(map[string]interface{})
				for _, ep := range []string{"github.com", "enterprise"} {
					if m, still := eps[ep].(map[string]interface{}); still {
						for _, field := range []string{"context_window", "max_tokens"} {
							if _, f := m[field]; f {
								t.Errorf("%s: %s survived zero-clear tombstone (dot-path shattering, #5779)", ep, field)
							}
						}
					}
				}
			}
		}
	}
}

func TestIssue5779StringTombstoneDotEndpointName(t *testing.T) {
	// Same bijectivity for the string-tombstone chain: snapshot + collect must
	// agree on separator so a cleared dotted key is deleted from existingRaw.
	disk := map[string]interface{}{
		"vendors": map[string]interface{}{
			"github-copilot": map[string]interface{}{
				"endpoints": map[string]interface{}{
					"github.com": map[string]interface{}{
						"display_name": "GH",
					},
				},
			},
		},
	}
	snap := snapshotDiskStringKeys(disk)
	memory := map[string]interface{}{
		"vendors": map[string]interface{}{
			"github-copilot": map[string]interface{}{
				"endpoints": map[string]interface{}{
					"github.com": map[string]interface{}{
						"display_name": "", // cleared in memory
					},
				},
			},
		},
	}
	cleared := collectEmptyStringPaths(memory)

	for p := range cleared {
		if !strings.Contains(p, tombstonePathSep) {
			t.Fatalf("cleared path %q missing tombstone separator", p)
		}
	}
	applyClearedTombstones(disk, snap, cleared)

	if vendorsAny, ok := disk["vendors"].(map[string]interface{}); ok {
		if gcAny, ok2 := vendorsAny["github-copilot"].(map[string]interface{}); ok2 {
			if epsAny, ok3 := gcAny["endpoints"].(map[string]interface{}); ok3 {
				if epAny, ok4 := epsAny["github.com"].(map[string]interface{}); ok4 {
					if _, still := epAny["display_name"]; still {
						t.Error("display_name survived string tombstone for dot-containing endpoint name (#5779)")
					}
				}
			}
		}
	}
}

func TestIssue5779PathSeparatorNotInKeys(t *testing.T) {
	if strings.ContainsAny("github.com\x01", tombstonePathSep) {
		t.Fatal("sanity: separator assumption broken")
	}
}
