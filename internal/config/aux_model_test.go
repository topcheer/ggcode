package config

import "testing"

func TestDefaultSmallModel(t *testing.T) {
	// vendor with a generated small-model default
	if got := DefaultSmallModel("xiaomi-mimo"); got != "MiMo-V2.5" {
		t.Fatalf("DefaultSmallModel(xiaomi-mimo) = %q, want MiMo-V2.5", got)
	}
	// vendors without one and unknown/empty vendors stay empty
	for _, vendor := range []string{"", "no-such-vendor", "anthropic"} {
		if got := DefaultSmallModel(vendor); got != "" {
			t.Errorf("DefaultSmallModel(%q) = %q, want empty", vendor, got)
		}
	}
}
