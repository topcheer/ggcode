package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

// #2583: the desktop_control schema promised that the `app` parameter targets
// the window for set_window_bounds, but all three platforms ignore it (macOS
// and Linux fall back to the frontmost/active window, Windows requires a
// title match via `text`). Until per-app window addressing lands, the schema
// must disclose the real targeting contract (`text` = title substring on
// Linux/Windows) so models do not call set_window_bounds with app= and hit a
// silent-wrong-target or hard-error path. These assertions pin the schema
// disclosure; they do not change tool behavior.
func TestIssue2583SchemaDisclosesSetWindowBoundsTargeting(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	raw := (DesktopControlTool{}).Parameters()
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("desktop_control parameters schema is not valid JSON: %v", err)
	}

	// 1. The `text` param must document its set_window_bounds role.
	textDesc, ok := schema.Properties["text"]
	if !ok {
		t.Fatal("schema is missing the text property")
	}
	if !strings.Contains(textDesc.Description, "set_window_bounds") {
		t.Errorf("text param description must disclose the set_window_bounds title-substring role, got: %q", textDesc.Description)
	}
	if !strings.Contains(textDesc.Description, "title substring") {
		t.Errorf("text param description must say 'title substring' for set_window_bounds, got: %q", textDesc.Description)
	}

	// 2. The `app` param must not promise set_window_bounds targeting it does
	//    not honor (the original false promise: "whose window to resize").
	appDesc, ok := schema.Properties["app"]
	if !ok {
		t.Fatal("schema is missing the app property")
	}
	appDescLower := strings.ToLower(appDesc.Description)
	if strings.Contains(appDescLower, "whose window to resize") {
		t.Errorf("app param description still promises window targeting for set_window_bounds, got: %q", appDesc.Description)
	}
	if !strings.Contains(appDescLower, "not yet honored") {
		t.Errorf("app param description must flag set_window_bounds as not yet honored, got: %q", appDesc.Description)
	}

	// 3. The tool-level description must explain target selection per platform.
	toolDesc := (DesktopControlTool{}).Description()
	for _, want := range []string{"Target selection", "frontmost", "title substring"} {
		if !strings.Contains(toolDesc, want) {
			t.Errorf("tool description must explain set_window_bounds target selection (missing %q)", want)
		}
	}
}
