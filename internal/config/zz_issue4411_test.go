package config

// zz_issue4411_test.go - companion tests for #4411: instance-level vendors
// override deletion resurrecting as a zombie vendor because diffVendors
// emits no nil deletion markers, plus the diffA2A same-family nested reset
// hole. Mirrors the #2936/#4315 nil-marker precedents.

import "testing"

func TestIssue4411VendorPartialDeleteMarker(t *testing.T) {
	// On disk: vendors A (deleted by user) + B (still overridden).
	// Delta carries only B - A must get a nil deletion marker.
	existing := map[string]interface{}{
		"vendors": map[string]interface{}{
			"A": map[string]interface{}{"api_base": "https://a.example"},
			"B": map[string]interface{}{"api_base": "https://b.example"},
		},
	}
	delta := map[string]interface{}{
		"vendors": map[string]interface{}{
			"B": map[string]interface{}{"api_base": "https://b.example"},
		},
	}
	applyInstanceResets(existing, delta, map[string]bool{"vendors": true})

	vd, ok := delta["vendors"].(map[string]interface{})
	if !ok {
		t.Fatalf("vendors section delta missing: %#v", delta["vendors"])
	}
	if got, exists := vd["A"]; !exists || got != nil {
		t.Fatalf("deleted vendor A must carry nil marker, got %#v (exists=%v)", got, exists)
	}
	if got, exists := vd["B"]; !exists || got == nil {
		t.Fatalf("kept vendor B must stay untouched, got %#v (exists=%v)", got, exists)
	}
}

func TestIssue4411VendorAllDeletedCreatesSection(t *testing.T) {
	// diffVendors early-returns on len(current)==0: delta has NO vendors
	// key at all, yet stale disk keys remain. The reset layer must create
	// the section delta to carry the deletions.
	existing := map[string]interface{}{
		"vendors": map[string]interface{}{
			"A": map[string]interface{}{"api_base": "https://a.example"},
			"B": map[string]interface{}{"api_base": "https://b.example"},
		},
	}
	delta := map[string]interface{}{}
	applyInstanceResets(existing, delta, map[string]bool{"vendors": true})

	vd, ok := delta["vendors"].(map[string]interface{})
	if !ok {
		t.Fatalf("section delta must be created for wholesale deletion, got %#v", delta["vendors"])
	}
	for _, k := range []string{"A", "B"} {
		if got, exists := vd[k]; !exists || got != nil {
			t.Fatalf("vendor %s must carry nil marker, got %#v (exists=%v)", k, got, exists)
		}
	}
}

func TestIssue4411VendorGateRespected(t *testing.T) {
	// #734 regression probe: without instanceFields["vendors"] the disk
	// vendors section is NOT governed by this session's delta and must be
	// left alone.
	existing := map[string]interface{}{
		"vendors": map[string]interface{}{
			"A": map[string]interface{}{"api_base": "https://a.example"},
		},
	}
	delta := map[string]interface{}{}
	applyInstanceResets(existing, delta, map[string]bool{"language": true})

	if _, touched := delta["vendors"]; touched {
		t.Fatalf("vendors must not be touched without the governance gate: %#v", delta["vendors"])
	}
}

func TestIssue4411VendorDeletionReachesDeepMerge(t *testing.T) {
	// End-to-end resurrection-chain break: the nil marker must make
	// deepMergeYAMLMaps actually delete the stale disk key.
	existing := map[string]interface{}{
		"vendors": map[string]interface{}{
			"A": map[string]interface{}{"api_base": "https://a.example"},
			"B": map[string]interface{}{"api_base": "https://b.example"},
		},
	}
	delta := map[string]interface{}{} // all vendors deleted
	applyInstanceResets(existing, delta, map[string]bool{"vendors": true})
	deepMergeYAMLMaps(existing, delta)

	merged, ok := existing["vendors"].(map[string]interface{})
	if !ok {
		t.Fatalf("vendors section must exist in merged result: %#v", existing["vendors"])
	}
	if len(merged) != 0 {
		t.Fatalf("all vendors were deleted; merged section must be empty, got %#v", merged)
	}
}

func TestIssue4411A2ANestedResetMarker(t *testing.T) {
	// diffA2A records only current != global fields. port edited back to
	// the global default vanishes from the delta while the stale disk
	// value survives - the #2936 hole one level down.
	existing := map[string]interface{}{
		"a2a": map[string]interface{}{
			"port": 9999,
		},
	}
	delta := map[string]interface{}{
		"a2a": map[string]interface{}{"host": "0.0.0.0"},
	}
	applyInstanceResets(existing, delta, map[string]bool{"a2a": true})

	ad, ok := delta["a2a"].(map[string]interface{})
	if !ok {
		t.Fatalf("a2a section delta missing: %#v", delta["a2a"])
	}
	if got, exists := ad["port"]; !exists || got != nil {
		t.Fatalf("reset a2a.port must carry nil marker, got %#v (exists=%v)", got, exists)
	}
	if got, exists := ad["host"]; !exists || got == nil {
		t.Fatalf("explicit a2a.host override must stay, got %#v (exists=%v)", got, exists)
	}
}

func TestIssue4411A2AWholeSectionReset(t *testing.T) {
	// Every a2a field back at global defaults: no a2a key in the delta,
	// stale disk port must still get a deletion marker via the created
	// section delta.
	existing := map[string]interface{}{
		"a2a": map[string]interface{}{"port": 9999, "host": "stale"},
	}
	delta := map[string]interface{}{}
	applyInstanceResets(existing, delta, map[string]bool{"a2a": true})

	ad, ok := delta["a2a"].(map[string]interface{})
	if !ok {
		t.Fatalf("a2a section delta must be created, got %#v", delta["a2a"])
	}
	for _, k := range []string{"port", "host"} {
		if got, exists := ad[k]; !exists || got != nil {
			t.Fatalf("a2a.%s must carry nil marker, got %#v (exists=%v)", k, got, exists)
		}
	}
}
