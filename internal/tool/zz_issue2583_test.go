package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

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

	toolDesc := (DesktopControlTool{}).Description()
	for _, want := range []string{"Target selection", "frontmost", "title substring"} {
		if !strings.Contains(toolDesc, want) {
			t.Errorf("tool description must explain set_window_bounds target selection (missing %q)", want)
		}
	}
}
