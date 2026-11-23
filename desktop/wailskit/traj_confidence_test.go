package wailskit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTrajConfidenceOverviewEmptyWorkspace(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Fatal(err)
	}
	b.workingDir = t.TempDir()
	ov := b.TrajConfidenceOverview()
	if ov.Total != 0 || ov.HasLearnings || ov.AvgConfidence != 0 {
		t.Fatalf("empty workspace should yield zero overview, got %+v", ov)
	}
	if ov.Top == nil {
		t.Fatal("Top must be an empty slice, not nil, for stable JSON")
	}
	raw, err := json.Marshal(ov)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"total", "avgConfidence", "top", "hasLearnings"} {
		if !strings.Contains(string(raw), `"`+field+`"`) {
			t.Errorf("JSON missing field %q: %s", field, raw)
		}
	}
	for _, gone := range []string{"injecting", "injects", "holdout"} {
		if strings.Contains(string(raw), `"`+gone+`"`) {
			t.Errorf("JSON still carries retired injection facet %q: %s", gone, raw)
		}
	}
}
