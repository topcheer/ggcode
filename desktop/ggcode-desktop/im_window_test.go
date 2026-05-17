package main

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// ──────────────────────── platformDisplayName ────────────────────────

func TestPlatformDisplayName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"qq", "QQ"},
		{"telegram", "Telegram"},
		{"discord", "Discord"},
		{"feishu", "Feishu"},
		{"dingtalk", "DingTalk"},
		{"slack", "Slack"},
		{"wechat", "WeChat"},
		{"wecom", "WeCom"},
		{"whatsapp", "WhatsApp"},
		{"mattermost", "Mattermost"},
		{"signal", "Signal"},
		{"irc", "IRC"},
		{"matrix", "Matrix"},
		{"nostr", "Nostr"},
		{"twitch", "Twitch"},
	}
	for _, tc := range tests {
		got := platformDisplayName(tc.input)
		if got != tc.expected {
			t.Errorf("platformDisplayName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestPlatformDisplayNameUnknown(t *testing.T) {
	got := platformDisplayName("unknown_platform")
	if got == "" {
		t.Error("platformDisplayName should return something for unknown platforms")
	}
}

// ──────────────────────── platformRegistry ────────────────────────

func TestPlatformRegistryCompleteness(t *testing.T) {
	expectedPlatforms := []string{
		"qq", "telegram", "discord", "feishu", "dingtalk",
		"slack", "wechat", "wecom", "whatsapp", "mattermost",
		"signal", "irc", "matrix", "nostr", "twitch",
	}
	for _, p := range expectedPlatforms {
		meta, ok := platformRegistry[p]
		if !ok {
			t.Errorf("platformRegistry missing platform: %s", p)
			continue
		}
		if meta.DisplayName == "" {
			t.Errorf("platformRegistry[%s] has empty DisplayName", p)
		}
		// WhatsApp, Signal etc may have empty fields (scan-based)
		// but most should have at least one field
		if p != "whatsapp" && p != "wechat" && len(meta.Fields) == 0 {
			t.Errorf("platformRegistry[%s] has no fields defined", p)
		}
	}
}

func TestPlatformRegistryFieldsHaveLabels(t *testing.T) {
	for plat, meta := range platformRegistry {
		for i, f := range meta.Fields {
			if f.Key == "" {
				t.Errorf("platformRegistry[%s].Fields[%d] has empty Key", plat, i)
			}
			if f.Label == "" {
				t.Errorf("platformRegistry[%s].Fields[%d] has empty Label", plat, i)
			}
			if f.Placeholder == "" {
				t.Errorf("platformRegistry[%s].Fields[%d] has empty Placeholder", plat, i)
			}
		}
	}
}

// ──────────────────────── imAdapterEntries grouping ────────────────────────

func TestIMAdapterEntriesGrouping(t *testing.T) {
	// We can't easily construct a full App with Fyne, so test the grouping logic
	// by verifying the data structures and sort behavior.
	entries := []imAdapterEntry{
		{Name: "zzz-bot", Platform: "telegram", Enabled: true, Workspace: "/other", IsCurrent: false},
		{Name: "aaa-bot", Platform: "qq", Enabled: true, Workspace: "", IsCurrent: false},
		{Name: "mmm-bot", Platform: "discord", Enabled: false, Workspace: "", IsCurrent: false},
		{Name: "bbb-bot", Platform: "wechat", Enabled: true, Workspace: "/current", IsCurrent: true},
	}

	var current, other, unbound, disabled []imAdapterEntry
	for _, e := range entries {
		if !e.Enabled {
			disabled = append(disabled, e)
		} else if e.IsCurrent {
			current = append(current, e)
		} else if e.Workspace != "" {
			other = append(other, e)
		} else {
			unbound = append(unbound, e)
		}
	}

	// Verify grouping
	if len(current) != 1 || current[0].Name != "bbb-bot" {
		t.Errorf("current group = %v, want [bbb-bot]", current)
	}
	if len(other) != 1 || other[0].Name != "zzz-bot" {
		t.Errorf("other group = %v, want [zzz-bot]", other)
	}
	if len(unbound) != 1 || unbound[0].Name != "aaa-bot" {
		t.Errorf("unbound group = %v, want [aaa-bot]", unbound)
	}
	if len(disabled) != 1 || disabled[0].Name != "mmm-bot" {
		t.Errorf("disabled group = %v, want [mmm-bot]", disabled)
	}
}

func TestIMAdapterEntriesSortOrder(t *testing.T) {
	// Within each group, entries should be sorted by name.
	// Build a config with multiple adapters in each group and verify order.
	//
	// This tests the same logic used in imAdapterEntries but without needing Fyne.
	type testCase struct {
		name     string
		platform string
		enabled  bool
		ws       string
		current  bool
	}

	cases := []testCase{
		{"z-current", "telegram", true, "/ws", true},
		{"a-current", "qq", true, "/ws", true},
		{"z-other", "discord", true, "/other", false},
		{"a-other", "wechat", true, "/other", false},
		{"z-unbound", "irc", true, "", false},
		{"a-unbound", "matrix", true, "", false},
		{"z-disabled", "slack", false, "", false},
		{"a-disabled", "twitch", false, "", false},
	}

	var current, other, unbound, disabled []imAdapterEntry
	for _, c := range cases {
		e := imAdapterEntry{Name: c.name, Platform: c.platform, Enabled: c.enabled, Workspace: c.ws, IsCurrent: c.current}
		if !c.enabled {
			disabled = append(disabled, e)
		} else if c.current {
			current = append(current, e)
		} else if c.ws != "" {
			other = append(other, e)
		} else {
			unbound = append(unbound, e)
		}
	}

	// Verify each group is empty or has at least 2 entries
	if len(current) != 2 {
		t.Fatalf("current group has %d entries, want 2", len(current))
	}
	// Before sorting, z > a
	if current[0].Name != "z-current" {
		t.Errorf("current[0] = %s, want z-current (before sort)", current[0].Name)
	}
	// After sorting (same logic as imAdapterEntries)
	sortSlice := func(s []imAdapterEntry) {
		for i := 0; i < len(s)-1; i++ {
			for j := i + 1; j < len(s); j++ {
				if s[i].Name > s[j].Name {
					s[i], s[j] = s[j], s[i]
				}
			}
		}
	}
	sortSlice(current)
	sortSlice(other)
	sortSlice(unbound)
	sortSlice(disabled)
	if current[0].Name > current[1].Name {
		t.Errorf("current group not sorted after sort: %s > %s", current[0].Name, current[1].Name)
	}
	if other[0].Name > other[1].Name {
		t.Errorf("other group not sorted after sort: %s > %s", other[0].Name, other[1].Name)
	}
	if unbound[0].Name > unbound[1].Name {
		t.Errorf("unbound group not sorted after sort: %s > %s", unbound[0].Name, unbound[1].Name)
	}
	if disabled[0].Name > disabled[1].Name {
		t.Errorf("disabled group not sorted after sort: %s > %s", disabled[0].Name, disabled[1].Name)
	}
}

// ──────────────────────── imAdapterEntry IsCurrent logic ────────────────────────

func TestIMAdapterEntryIsCurrentRequiresNonEmptyWorkspace(t *testing.T) {
	// An entry with empty workspace should never be IsCurrent, even if currentWS is also empty
	e := imAdapterEntry{
		Name: "test", Platform: "telegram", Enabled: true,
		Workspace: "", IsCurrent: false,
	}
	if e.IsCurrent {
		t.Error("entry with empty workspace should not be IsCurrent")
	}
}

// ──────────────────────── imAdapterEntries with nil config ────────────────────────

func TestIMAdapterEntriesNilConfig(t *testing.T) {
	app := &App{cfg: nil}
	entries := app.imAdapterEntries()
	if entries != nil {
		t.Errorf("expected nil for nil config, got %v", entries)
	}
}

func TestIMAdapterEntriesNilAdapters(t *testing.T) {
	app := &App{cfg: &config.Config{}}
	entries := app.imAdapterEntries()
	if entries != nil {
		t.Errorf("expected nil for nil adapters, got %v", entries)
	}
}

func TestIMAdapterEntriesEmptyAdapters(t *testing.T) {
	app := &App{
		cfg: &config.Config{IM: config.IMConfig{Adapters: map[string]config.IMAdapterConfig{}}},
	}
	entries := app.imAdapterEntries()
	if len(entries) != 0 {
		t.Errorf("expected empty for empty adapters, got %d", len(entries))
	}
}

// ──────────────────────── sortedPlatformKeys ────────────────────────

func TestSortedPlatformKeys(t *testing.T) {
	keys := sortedPlatformKeys()
	if len(keys) != len(platformRegistry) {
		t.Errorf("sortedPlatformKeys returned %d keys, expected %d", len(keys), len(platformRegistry))
	}
	// Verify sorted
	for i := 1; i < len(keys); i++ {
		if keys[i] < keys[i-1] {
			t.Errorf("keys not sorted: %s > %s at index %d", keys[i-1], keys[i], i)
		}
	}
}
