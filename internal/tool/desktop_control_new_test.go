package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeModifiers(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "cmd", want: "cmd"},
		{in: "Command", want: "cmd"},
		{in: "meta", want: "cmd"},
		{in: "super", want: "cmd"},
		{in: "ctrl+shift", want: "ctrl+shift"},
		{in: "Ctrl + Option", want: "ctrl+alt"},
		{in: "alt", want: "alt"},
		{in: "option", want: "alt"},
		{in: "opt", want: "alt"},
		{in: "fn", want: "fn"},
		{in: "cmd+cmd", want: "cmd"},                                 // dedup
		{in: "cmd+shift+ctrl+alt+fn", want: "cmd+shift+ctrl+alt+fn"}, // all five
		{in: "", wantErr: true},                                      // empty
		{in: "cmd++shift", wantErr: true},                            // empty component
		{in: "cmd+c", wantErr: true},                                 // non-modifier key
		{in: "superkey", wantErr: true},                              // typo
	}
	for _, c := range cases {
		mods, err := normalizeModifiers(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeModifiers(%q): expected error, got %v", c.in, mods)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeModifiers(%q): unexpected error %v", c.in, err)
			continue
		}
		if got := strings.Join(mods, "+"); got != c.want {
			t.Errorf("normalizeModifiers(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseMenuPath(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: "File > Save", want: []string{"File", "Save"}},
		{in: "View > Sort By > Name", want: []string{"View", "Sort By", "Name"}},
		{in: " File >  Export… ", want: []string{"File", "Export…"}},
		{in: "File", wantErr: true}, // single item: nothing to select
		{in: "  ", wantErr: true},   // empty
		{in: " > ", wantErr: true},  // only separators
	}
	for _, c := range cases {
		got, err := parseMenuPath(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseMenuPath(%q): expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseMenuPath(%q): unexpected error %v", c.in, err)
			continue
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("parseMenuPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestDesktopControlNewActionValidation exercises parameter validation that
// runs on every platform BEFORE any OS automation fires, so these tests are
// safe in CI without accessibility permissions.
func TestDesktopControlNewActionValidation(t *testing.T) {
	tool := DesktopControlTool{}

	// hold_key without duration must error before anything is held.
	input, _ := json.Marshal(map[string]any{
		"action": "hold_key", "text": "shift", "duration": 0,
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for hold_key with zero duration")
	}

	// hold_key over the cap must error.
	input, _ = json.Marshal(map[string]any{
		"action": "hold_key", "text": "shift", "duration": 60000,
	})
	_, err = tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for hold_key over 30s cap")
	}

	// modifier_click with a bogus modifier must error, not click.
	input, _ = json.Marshal(map[string]any{
		"action": "modifier_click", "x": 10, "y": 10, "text": "cmd+junk",
	})
	_, err = tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for invalid modifier in modifier_click")
	}

	// open without a target must error.
	input, _ = json.Marshal(map[string]any{"action": "open"})
	_, err = tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for open without text")
	}

	// set_window_bounds without dimensions must error.
	input, _ = json.Marshal(map[string]any{"action": "set_window_bounds", "x": 0, "y": 0})
	_, err = tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for set_window_bounds without width/height")
	}
}

// TestDesktopControlSchemaCoversNewActions pins that every new action is a
// valid enum value in the JSON schema — the agent can only call what the
// schema advertises.
func TestDesktopControlSchemaCoversNewActions(t *testing.T) {
	var schema struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(DesktopControlTool{}.Parameters(), &schema); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	have := map[string]bool{}
	for _, e := range schema.Properties.Action.Enum {
		have[e] = true
	}
	for _, a := range []string{
		"triple_click", "modifier_click", "mouse_position",
		"set_window_bounds", "open", "menu_select",
		"middle_click", "mouse_down", "mouse_up", "hold_key",
	} {
		if !have[a] {
			t.Errorf("action %q missing from schema enum", a)
		}
	}
}
