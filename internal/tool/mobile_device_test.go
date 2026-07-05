package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAndroidXMLParsing(t *testing.T) {
	xmlData := `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node text="" resource-id="" class="android.widget.FrameLayout" package="com.example.app" content-desc="" bounds="[0,0][1080,2400]">
    <node text="Login" resource-id="com.example.app:id/loginButton" class="android.widget.Button" content-desc="Login Button" bounds="[100,500][400,600]" />
    <node text="" resource-id="com.example.app:id/emailField" class="android.widget.EditText" content-desc="Email" bounds="[100,300][800,400]" />
    <node text="Welcome" resource-id="com.example.app:id/title" class="android.widget.TextView" content-desc="" bounds="[100,100][800,200]" />
  </node>
</hierarchy>`

	root := parseAndroidUIXML(xmlData)
	if root == nil {
		t.Fatal("expected non-nil root element")
	}
	if len(root.Children) == 0 {
		t.Fatal("expected root to have children")
	}
	foundButton := false
	for _, child := range root.Children {
		if child.Label == "Login" && child.Type == "button" {
			foundButton = true
			if child.Rect == nil {
				t.Error("button should have a rect")
			}
		}
	}
	if !foundButton {
		t.Error("expected to find Login button")
	}
}

func TestFormatSnapshot(t *testing.T) {
	root := &uiElement{
		Type: "view",
		Rect: &uiRect{X: 0, Y: 0, Width: 1080, Height: 2400},
		Children: []*uiElement{
			{Type: "button", Label: "Login", Rect: &uiRect{X: 100, Y: 500, Width: 300, Height: 100}},
			{Type: "text", Label: "Welcome", Rect: &uiRect{X: 100, Y: 100, Width: 700, Height: 100}},
		},
	}
	output := formatSnapshot(root, "Platform: Android")
	if !strings.Contains(output, "@e1") {
		t.Error("expected @e1 reference")
	}
	if !strings.Contains(output, "[button]") {
		t.Error("expected [button] type")
	}
	if !strings.Contains(output, "\"Login\"") {
		t.Error("expected Login label")
	}
}

func TestFindElementByID(t *testing.T) {
	root := &uiElement{
		Children: []*uiElement{
			{ID: "@e1", Type: "button", Label: "Login", Rect: &uiRect{X: 100, Y: 500, Width: 300, Height: 100}},
		},
	}
	found := findElementByID(root, "@e1")
	if found == nil {
		t.Fatal("expected to find @e1")
	}
	if found.Label != "Login" {
		t.Errorf("expected Login, got %s", found.Label)
	}
}

func TestAndroidKeyCode(t *testing.T) {
	tests := []struct{ input, expected string }{
		{"home", "3"}, {"back", "4"}, {"enter", "66"},
		{"volume_up", "24"}, {"HOME", "3"}, {"123", "123"},
	}
	for _, tt := range tests {
		if got := androidKeyCode(tt.input); got != tt.expected {
			t.Errorf("androidKeyCode(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSimplifyAndroidClass(t *testing.T) {
	tests := []struct{ input, expected string }{
		{"android.widget.Button", "button"},
		{"android.widget.TextView", "text"},
		{"android.widget.EditText", "editText"},
		{"android.widget.FrameLayout", "frame"},
		{"android.widget.LinearLayout", "linear"},
	}
	for _, tt := range tests {
		if got := simplifyAndroidClass(tt.input); got != tt.expected {
			t.Errorf("simplifyAndroidClass(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMobileDeviceParamsParsing(t *testing.T) {
	raw := `{"action":"tap","platform":"android","x":100,"y":200}`
	var p mobileDeviceParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if p.Action != "tap" || p.X != 100 || p.Y != 200 {
		t.Errorf("unexpected values: %+v", p)
	}
}

func TestMobileDeviceToolName(t *testing.T) {
	if (&MobileDeviceTool{}).Name() != "mobile_device" {
		t.Error("expected mobile_device")
	}
}

func TestMobileDeviceToolParameters(t *testing.T) {
	params := (&MobileDeviceTool{}).Parameters()
	var schema map[string]interface{}
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Error("expected type=object")
	}
}

func TestRectCenter(t *testing.T) {
	cx, cy := (&uiRect{X: 100, Y: 200, Width: 50, Height: 60}).center()
	if cx != 125 || cy != 230 {
		t.Errorf("expected (125,230), got (%d,%d)", cx, cy)
	}
}

func TestXMLAttr(t *testing.T) {
	attrs := `text="Hello" resource-id="com.example:id/btn"`
	if v := xmlAttr(attrs, "text"); v != "Hello" {
		t.Errorf("expected Hello, got %s", v)
	}
	if v := xmlAttr(attrs, "nonexistent"); v != "" {
		t.Errorf("expected empty, got %s", v)
	}
}

func TestTrimTree(t *testing.T) {
	root := &uiElement{
		Rect: &uiRect{X: 0, Y: 0, Width: 100, Height: 100},
		Children: []*uiElement{
			{Type: "button", Label: "OK", Rect: &uiRect{X: 10, Y: 10, Width: 20, Height: 20}},
			{Type: "view"},
		},
	}
	trimTree(root, 0)
	if len(root.Children) != 1 {
		t.Errorf("expected 1 child after trim, got %d", len(root.Children))
	}
}
