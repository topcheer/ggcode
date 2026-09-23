package tool

// Tests for the pure parsing/normalization helpers in mobile_adb.go
// (sa-141 coverage round). These run without any adb binary or device.

import (
	"strings"
	"testing"
)

func TestExtractTransportIDSa141(t *testing.T) {
	if got := extractTransportID([]string{"emulator-5554", "device", "product:sdk", "transport_id:42"}); got != "42" {
		t.Fatalf("extractTransportID = %q, want 42", got)
	}
	if got := extractTransportID([]string{"emulator-5554", "device"}); got != "" {
		t.Fatalf("extractTransportID without transport = %q, want empty", got)
	}
	if got := extractTransportID(nil); got != "" {
		t.Fatalf("extractTransportID(nil) = %q, want empty", got)
	}
	// Prefix alone with empty id still returns the trimmed remainder.
	if got := extractTransportID([]string{"transport_id:"}); got != "" {
		t.Fatalf("extractTransportID(empty id) = %q, want empty", got)
	}
}

func TestAdbDeviceArgsSa141(t *testing.T) {
	if got := adbDeviceArgs(""); got != nil {
		t.Fatalf("adbDeviceArgs(\"\") = %v, want nil", got)
	}
	if got := adbDeviceArgs("7"); len(got) != 2 || got[0] != "-t" || got[1] != "7" {
		t.Fatalf("adbDeviceArgs(numeric) = %v, want [-t 7]", got)
	}
	if got := adbDeviceArgs("emulator-5554"); len(got) != 2 || got[0] != "-s" || got[1] != "emulator-5554" {
		t.Fatalf("adbDeviceArgs(serial) = %v, want [-s emulator-5554]", got)
	}
}

func TestParseAndroidUIXMLSa141(t *testing.T) {
	xmlData := `<hierarchy rotation="0">` +
		`<node class="android.widget.Button" text="OK" bounds="[10,20][110,70]"/>` +
		`<node content-desc="Back" bounds="[0,0][50,50]"/>` +
		`<node class="android.widget.FrameLayout"/>` +
		`</hierarchy>`
	root := parseAndroidUIXML(xmlData)
	if root == nil {
		t.Fatal("parseAndroidUIXML returned nil for valid hierarchy")
	}
	if root.Type != "button" || root.Label != "OK" {
		t.Fatalf("root = type %q label %q, want button/OK", root.Type, root.Label)
	}
	if root.Rect == nil || root.Rect.X != 10 || root.Rect.Y != 20 || root.Rect.Width != 100 || root.Rect.Height != 50 {
		t.Fatalf("root.Rect = %+v, want {10 20 100 50}", root.Rect)
	}
	// The empty FrameLayout node must be dropped (no label, no rect).
	if len(root.Children) != 1 || root.Children[0].Label != "Back" {
		t.Fatalf("children = %+v, want single Back child", root.Children)
	}
}

func TestParseAndroidUIXMLGarbageSa141(t *testing.T) {
	for _, in := range []string{"", "not xml at all", "<hierarchy></hierarchy>"} {
		if got := parseAndroidUIXML(in); got != nil {
			t.Fatalf("parseAndroidUIXML(%q) = %+v, want nil", in, got)
		}
	}
}

func TestParseAndroidNodeLabelFallbackSa141(t *testing.T) {
	// text wins over content-desc and resource-id.
	el := parseAndroidNode(`text="Save" content-desc="save-btn" resource-id="com.app:id/save"`)
	if el == nil || el.Label != "Save" {
		t.Fatalf("label = %v, want Save (text precedence)", el)
	}
	// content-desc used when text empty.
	el = parseAndroidNode(`content-desc="Search" resource-id="com.app:id/q"`)
	if el == nil || el.Label != "Search" {
		t.Fatalf("label = %v, want Search (content-desc fallback)", el)
	}
	// resource-id used when both empty.
	el = parseAndroidNode(`resource-id="com.app:id/username"`)
	if el == nil || el.Label != "com.app:id/username" {
		t.Fatalf("label = %v, want resource-id fallback", el)
	}
}

func TestParseAndroidNodeSkipsUselessSa141(t *testing.T) {
	if el := parseAndroidNode(`class="android.widget.FrameLayout"`); el != nil {
		t.Fatalf("container node with no label/rect = %+v, want nil", el)
	}
}

func TestSimplifyAndroidClassSa141(t *testing.T) {
	cases := map[string]string{
		"android.widget.Button":   "button",
		"android.widget.TextView": "text",
		"com.test.LinearLayout":   "linear",
		"FrameLayout":             "frame",
		"View":                    "view",   // #839: plain View must not panic
		"android.widget.View":     "widget", // #839: empty last segment falls back to package
		"ImageView":               "image",
	}
	for in, want := range cases {
		if got := simplifyAndroidClass(in); got != want {
			t.Errorf("simplifyAndroidClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestXmlAttrSa141(t *testing.T) {
	attrs := `class="android.widget.Button" text="  hi  " bounds="[0,0][1,1]"`
	if got := xmlAttr(attrs, "text"); got != "hi" {
		t.Fatalf("xmlAttr text = %q, want hi (trimmed)", got)
	}
	if got := xmlAttr(attrs, "missing"); got != "" {
		t.Fatalf("xmlAttr missing = %q, want empty", got)
	}
	if got := xmlAttr("", "text"); got != "" {
		t.Fatalf("xmlAttr empty attrs = %q, want empty", got)
	}
}

func TestTrimTreeSa141(t *testing.T) {
	root := &uiElement{ID: "@e0", Children: []*uiElement{
		{ID: "@empty"},               // no label/rect/children -> pruned
		{ID: "@keep", Label: "kept"}, // labeled -> kept
		{ID: "@branch", Children: []*uiElement{{ID: "@deep-empty"}}}, // has children -> kept, child pruned
	}}
	trimTree(root, 0)
	if len(root.Children) != 2 {
		t.Fatalf("after trim children = %d, want 2", len(root.Children))
	}
	for _, c := range root.Children {
		if c.ID == "@branch" && len(c.Children) != 0 {
			t.Fatalf("branch child not pruned: %+v", c.Children)
		}
	}
}

func TestAndroidKeyCodeSa141(t *testing.T) {
	cases := map[string]string{
		"home": "3", "back": "4", "menu": "82",
		"volume_up": "24", "volume_down": "25", "power": "26",
		"enter": "66", "delete": "67", "backspace": "67", "tab": "61",
		"escape": "111", "recent": "187", "app_switch": "187",
	}
	for in, want := range cases {
		if got := androidKeyCode(in); got != want {
			t.Errorf("androidKeyCode(%q) = %q, want %q", in, got, want)
		}
	}
	// Case-insensitive.
	if got := androidKeyCode("BACK"); got != "4" {
		t.Fatalf("androidKeyCode(\"BACK\") = %q, want 4", got)
	}
	// Unknown names pass through as raw keycodes.
	if got := androidKeyCode("123"); got != "123" {
		t.Fatalf("androidKeyCode raw = %q, want 123", got)
	}
}

func TestAndroidBackendResolveRefSa141(t *testing.T) {
	root := &uiElement{ID: "@e0", Rect: &uiRect{X: 10, Y: 20, Width: 100, Height: 50},
		Children: []*uiElement{
			{ID: "@e1", Rect: &uiRect{X: 0, Y: 0, Width: 40, Height: 20}},
			{ID: "@e2"}, // no rect
		}}
	a := &androidBackend{snapshots: map[string]*uiElement{"dev": root}}

	if _, _, err := a.resolveRef("dev", "e1"); err == nil || !strings.Contains(err.Error(), "expected @eN") {
		t.Fatalf("resolveRef bad prefix err = %v", err)
	}
	if _, _, err := a.resolveRef("other", "@e0"); err == nil || !strings.Contains(err.Error(), "prior snapshot") {
		t.Fatalf("resolveRef missing snapshot err = %v", err)
	}
	if _, _, err := a.resolveRef("dev", "@e9"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("resolveRef unknown ref err = %v", err)
	}
	if _, _, err := a.resolveRef("dev", "@e2"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("resolveRef rectless element err = %v", err)
	}
	x, y, err := a.resolveRef("dev", "@e1")
	if err != nil || x != 20 || y != 10 {
		t.Fatalf("resolveRef(@e1) = (%d,%d,%v), want (20,10,nil)", x, y, err)
	}
	x, y, err = a.resolveRef("dev", "@e0")
	if err != nil || x != 60 || y != 45 {
		t.Fatalf("resolveRef(@e0) = (%d,%d,%v), want (60,45,nil)", x, y, err)
	}
}
