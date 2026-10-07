package config

// #3531 probe: diffA2A must keep an explicit api_key CLEAR in the delta even
// when another auth field is being added in the same instance-scope delta -
// the old two competing writes for a2a.auth let the second wholesale
// overwrite the first and drop the clear.

import (
	"testing"
)

func TestIssue3531_ClearPlusOtherAuthKeepsClear(t *testing.T) {
	global := &A2AConfig{Auth: A2AAuthConfig{APIKey: "old-key"}}
	current := &A2AConfig{Auth: A2AAuthConfig{
		// api_key explicitly cleared AND oauth2 newly added in one delta.
		APIKey:  "",
		APIKeys: []string{"peer-a"},
	}}

	delta := map[string]interface{}{}
	(&Config{}).diffA2A(current, global, delta)

	auth, ok := delta["a2a"].(map[string]any)["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth delta missing: %+v", delta["a2a"])
	}
	// The clear must survive alongside the addition.
	if v, ok := auth["api_key"]; !ok || v != "" {
		t.Fatalf("explicit api_key clear lost from delta: %+v", auth)
	}
	if _, ok := auth["api_keys"]; !ok {
		t.Fatalf("api_keys addition lost: %+v", auth)
	}
}

func TestIssue3531_ClearOnlyStillInDelta(t *testing.T) {
	global := &A2AConfig{Auth: A2AAuthConfig{APIKey: "old-key"}}
	current := &A2AConfig{Auth: A2AAuthConfig{APIKey: ""}}

	delta := map[string]interface{}{}
	(&Config{}).diffA2A(current, global, delta)

	auth, ok := delta["a2a"].(map[string]any)["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth delta missing for a pure clear: %+v", delta)
	}
	if v, ok := auth["api_key"]; !ok || v != "" {
		t.Fatalf("pure clear must stay in delta: %+v", auth)
	}
}

func TestIssue3531_SetAndUnchanged(t *testing.T) {
	// Plain set still lands.
	global := &A2AConfig{Auth: A2AAuthConfig{APIKey: "a"}}
	current := &A2AConfig{Auth: A2AAuthConfig{APIKey: "b"}}
	delta := map[string]interface{}{}
	(&Config{}).diffA2A(current, global, delta)
	auth := delta["a2a"].(map[string]any)["auth"].(map[string]any)
	if auth["api_key"] != "b" {
		t.Fatalf("plain set lost: %+v", auth)
	}

	// Unchanged -> no auth delta at all.
	delta2 := map[string]interface{}{}
	(&Config{}).diffA2A(current, current, delta2)
	if a2a, ok := delta2["a2a"]; !ok {
		_ = a2a
	} else {
		if _, has := a2a.(map[string]any)["auth"]; has {
			t.Fatalf("unchanged auth must not appear in delta: %+v", a2a)
		}
	}
}
