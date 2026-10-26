package tool

import "testing"

// #4440 Bug 5: negative Amount/MaxDepth/TimeoutMs used to bypass the ==0
// defaults (schema "minimum" is not enforced by json.Unmarshal) and flow to
// the platform layer as-is. clampDesktopDefaults now lower-bounds them.
func TestIssue4440ClampDesktopDefaults(t *testing.T) {
	p := desktopParams{Amount: -3, MaxDepth: -1, TimeoutMs: -2500}
	clampDesktopDefaults(&p)
	if p.Amount != 1 || p.MaxDepth != 8 || p.TimeoutMs != 5000 {
		t.Fatalf("negatives must clamp to defaults, got %+v", p)
	}

	zero := desktopParams{}
	clampDesktopDefaults(&zero)
	if zero.Button != "left" || zero.Direction != "down" ||
		zero.Amount != 1 || zero.MaxDepth != 8 || zero.TimeoutMs != 5000 {
		t.Fatalf("zero values must keep defaults, got %+v", zero)
	}

	ok := desktopParams{Button: "middle", Direction: "up", Amount: 2, MaxDepth: 4, TimeoutMs: 800}
	clampDesktopDefaults(&ok)
	if ok.Button != "middle" || ok.Direction != "up" || ok.Amount != 2 ||
		ok.MaxDepth != 4 || ok.TimeoutMs != 800 {
		t.Fatalf("valid values must be preserved, got %+v", ok)
	}
}
