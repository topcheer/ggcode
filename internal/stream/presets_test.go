package stream

import (
	"testing"
)

func TestPresetByID(t *testing.T) {
	tests := []struct {
		id      string
		want    string
		wantNil bool
	}{
		{"youtube", "YouTube", false},
		{"bilibili", "Bilibili (哔哩哔哩)", false},
		{"twitch", "Twitch", false},
		{"nonexistent", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			p := PresetByID(tt.id)
			if tt.wantNil {
				if p != nil {
					t.Errorf("PresetByID(%q) should be nil", tt.id)
				}
			} else {
				if p == nil {
					t.Fatalf("PresetByID(%q) = nil, want %q", tt.id, tt.want)
				}
				if p.Name != tt.want {
					t.Errorf("Name = %q, want %q", p.Name, tt.want)
				}
			}
		})
	}
}

func TestPresetByIDReturnsPointer(t *testing.T) {
	p := PresetByID("youtube")
	if p == nil {
		t.Fatal("expected non-nil preset")
	}
	if p.ID != "youtube" {
		t.Errorf("ID = %q, want %q", p.ID, "youtube")
	}
	if p.URL == "" {
		t.Error("URL should not be empty")
	}
	if p.Protocol == "" {
		t.Error("Protocol should not be empty")
	}
}

func TestPresetNames(t *testing.T) {
	names := PresetNames()
	if len(names) != len(Presets) {
		t.Errorf("PresetNames() returned %d names, expected %d", len(names), len(Presets))
	}
	// Check that YouTube is in the list
	found := false
	for _, n := range names {
		if n == "YouTube" {
			found = true
			break
		}
	}
	if !found {
		t.Error("PresetNames() should contain 'YouTube'")
	}
}

func TestPresetsByRegion(t *testing.T) {
	byRegion := PresetsByRegion()

	intl, ok := byRegion["international"]
	if !ok {
		t.Fatal("expected 'international' region")
	}
	if len(intl) == 0 {
		t.Error("international region should have presets")
	}

	china, ok := byRegion["china"]
	if !ok {
		t.Fatal("expected 'china' region")
	}
	if len(china) == 0 {
		t.Error("china region should have presets")
	}
}

func TestAllPresetsHaveRequiredFields(t *testing.T) {
	for _, p := range Presets {
		t.Run(p.ID, func(t *testing.T) {
			if p.Name == "" {
				t.Error("Name is required")
			}
			if p.ID == "" {
				t.Error("ID is required")
			}
			if p.URL == "" {
				t.Error("URL is required")
			}
			if p.Region == "" {
				t.Error("Region is required")
			}
			if p.Protocol == "" {
				t.Error("Protocol is required")
			}
			if p.HelpURL == "" {
				t.Error("HelpURL is required")
			}
			if p.KeyHint == "" {
				t.Error("KeyHint is required")
			}
		})
	}
}

func TestPresetIDsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, p := range Presets {
		if seen[p.ID] {
			t.Errorf("duplicate preset ID: %q", p.ID)
		}
		seen[p.ID] = true
	}
}
