package config

import (
	"testing"
)

func TestIssue3531_ClearPlusOtherAuthKeepsClear(t *testing.T) {
	global := &A2AConfig{Auth: A2AAuthConfig{APIKey: "old-key"}}
	current := &A2AConfig{Auth: A2AAuthConfig{

		APIKey:  "",
		APIKeys: []string{"peer-a"},
	}}

	delta := map[string]interface{}{}
	(&Config{}).diffA2A(current, global, delta)

	auth, ok := delta["a2a"].(map[string]any)["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth delta missing: %+v", delta["a2a"])
	}

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

	global := &A2AConfig{Auth: A2AAuthConfig{APIKey: "a"}}
	current := &A2AConfig{Auth: A2AAuthConfig{APIKey: "b"}}
	delta := map[string]interface{}{}
	(&Config{}).diffA2A(current, global, delta)
	auth := delta["a2a"].(map[string]any)["auth"].(map[string]any)
	if auth["api_key"] != "b" {
		t.Fatalf("plain set lost: %+v", auth)
	}

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
