package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// alwaysAlive is a test override that treats all PIDs as alive.
func alwaysAlive(int) bool { return true }

func TestRegisterCreatesPIDFile(t *testing.T) {
	dir := t.TempDir()
	d := NewInstanceDetect(dir)
	d.checkAlive = alwaysAlive

	others, err := d.Register()
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(others) != 0 {
		t.Fatalf("expected 0 others, got %d", len(others))
	}

	// Verify PID file exists
	entries, _ := os.ReadDir(filepath.Join(dir, ".ggcode", instancesDir))
	if len(entries) != 1 {
		t.Fatalf("expected 1 PID file, got %d", len(entries))
	}

	// Verify contents
	data, _ := os.ReadFile(filepath.Join(dir, ".ggcode", instancesDir, entries[0].Name()))
	var info InstanceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("expected PID %d, got %d", os.Getpid(), info.PID)
	}
	if info.UUID == "" {
		t.Fatal("expected non-empty UUID")
	}
	if info.UUID != d.Info().UUID {
		t.Fatal("UUID mismatch")
	}
}

func TestUnregisterRemovesPIDFile(t *testing.T) {
	dir := t.TempDir()
	d := NewInstanceDetect(dir)
	d.checkAlive = alwaysAlive

	d.Register()
	instancesDir := filepath.Join(dir, ".ggcode", instancesDir)

	entries, _ := os.ReadDir(instancesDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 file after register, got %d", len(entries))
	}

	d.Unregister()

	entries, _ = os.ReadDir(instancesDir)
	if len(entries) != 0 {
		t.Fatalf("expected 0 files after unregister, got %d", len(entries))
	}
}

func TestRegisterCleansStaleFiles(t *testing.T) {
	dir := t.TempDir()
	instancesDir := filepath.Join(dir, ".ggcode", instancesDir)
	os.MkdirAll(instancesDir, 0o755)

	// Write a stale file with a PID that's definitely not alive.
	// Use the real isProcessAlive (default) which will return false for 999999999.
	staleInfo := InstanceInfo{
		PID:       999999999,
		UUID:      "stale-uuid-1234",
		StartedAt: time.Now().Add(-1 * time.Hour),
	}
	staleData, _ := json.Marshal(staleInfo)
	staleFile := filepath.Join(instancesDir, "999999999-stale-uu.json")
	os.WriteFile(staleFile, staleData, 0o644)

	d := NewInstanceDetect(dir)
	// Keep default checkAlive (real isProcessAlive) so stale PID is cleaned
	_, err := d.Register()
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Stale file should be removed
	entries, _ := os.ReadDir(instancesDir)
	for _, e := range entries {
		if e.Name() == "999999999-stale-uu.json" {
			t.Fatal("stale file should have been cleaned up")
		}
	}
	activeCount := 0
	for _, e := range entries {
		if !e.IsDir() {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected 1 active file, got %d", activeCount)
	}
}

func TestRegisterCleansCorruptedFiles(t *testing.T) {
	dir := t.TempDir()
	instancesDir := filepath.Join(dir, ".ggcode", instancesDir)
	os.MkdirAll(instancesDir, 0o755)

	corruptFile := filepath.Join(instancesDir, "12345-corrupt.json")
	os.WriteFile(corruptFile, []byte("not json at all"), 0o644)

	d := NewInstanceDetect(dir)
	d.checkAlive = alwaysAlive
	_, err := d.Register()
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := os.Stat(corruptFile); !os.IsNotExist(err) {
		t.Fatal("corrupted file should have been removed")
	}
}

func TestDetectMultipleInstances(t *testing.T) {
	dir := t.TempDir()
	instancesDir := filepath.Join(dir, ".ggcode", instancesDir)
	os.MkdirAll(instancesDir, 0o755)

	// Simulate a "first" instance with a fake PID
	firstInfo := InstanceInfo{
		PID:       11111,
		UUID:      "first-uuid-abcd",
		StartedAt: time.Now().Add(-5 * time.Minute),
	}
	firstData, _ := json.Marshal(firstInfo)
	os.WriteFile(filepath.Join(instancesDir, "11111-first-uu.json"), firstData, 0o644)

	// "Second" instance registers
	d := NewInstanceDetect(dir)
	d.checkAlive = alwaysAlive
	others, err := d.Register()
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(others) != 1 {
		t.Fatalf("expected 1 other instance, got %d", len(others))
	}
	if others[0].UUID != "first-uuid-abcd" {
		t.Fatalf("expected first instance UUID, got %s", others[0].UUID)
	}
}

func TestIsPrimary(t *testing.T) {
	dir := t.TempDir()
	instancesDir := filepath.Join(dir, ".ggcode", instancesDir)
	os.MkdirAll(instancesDir, 0o755)

	// No other instances → primary
	d := NewInstanceDetect(dir)
	d.checkAlive = alwaysAlive
	d.Register()
	if !d.IsPrimary() {
		t.Fatal("should be primary when no other instances")
	}

	// Add an older instance
	olderInfo := InstanceInfo{
		PID:       22222,
		UUID:      "older-uuid-1234",
		StartedAt: time.Now().Add(-10 * time.Minute),
	}
	olderData, _ := json.Marshal(olderInfo)
	os.WriteFile(filepath.Join(instancesDir, "22222-older-uu.json"), olderData, 0o644)

	if d.IsPrimary() {
		t.Fatal("should NOT be primary — older instance exists")
	}
}

func TestListInstances(t *testing.T) {
	dir := t.TempDir()
	instancesDir := filepath.Join(dir, ".ggcode", instancesDir)
	os.MkdirAll(instancesDir, 0o755)

	// Create fake instances with different PIDs
	infos := []InstanceInfo{
		{PID: 10001, UUID: "aaa-first-inst", StartedAt: time.Now().Add(-10 * time.Minute)},
		{PID: 10002, UUID: "bbb-second-ins", StartedAt: time.Now().Add(-5 * time.Minute)},
	}
	for _, info := range infos {
		data, _ := json.Marshal(info)
		os.WriteFile(filepath.Join(instancesDir, fmt.Sprintf("%d-%s.json", info.PID, info.UUID[:8])), data, 0o644)
	}

	d := NewInstanceDetect(dir)
	d.checkAlive = alwaysAlive
	d.Register()

	list := d.ListInstances()
	// Should include the 2 fakes + self = 3
	if len(list) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(list))
	}
	// Should be sorted by StartedAt
	if !list[0].StartedAt.Before(list[1].StartedAt) {
		t.Fatal("instances should be sorted by StartedAt ascending")
	}
	if !list[1].StartedAt.Before(list[2].StartedAt) {
		t.Fatal("instances should be sorted by StartedAt ascending")
	}
}

func TestUpdateHasActiveChannels(t *testing.T) {
	dir := t.TempDir()
	d := NewInstanceDetect(dir)
	d.checkAlive = alwaysAlive
	d.Register()

	if err := d.UpdateHasActiveChannels(true); err != nil {
		t.Fatalf("UpdateHasActiveChannels: %v", err)
	}

	list := d.ListInstances()
	found := false
	for _, info := range list {
		if info.UUID == d.Info().UUID {
			if !info.HasActiveChannels {
				t.Fatal("expected HasActiveChannels=true")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("self not found in instance list")
	}
}

func TestUnregisterIdempotent(t *testing.T) {
	dir := t.TempDir()
	d := NewInstanceDetect(dir)
	d.Unregister()
	d.Unregister()
}

func TestParsePIDFromFilename(t *testing.T) {
	tests := []struct {
		name    string
		want    int
		wantErr bool
	}{
		{"12345-abc12345.json", 12345, false},
		{"1-a.json", 1, false},
		{"badfile.json", 0, true},
	}
	for _, tt := range tests {
		got, err := parsePIDFromFilename(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("parsePIDFromFilename(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("parsePIDFromFilename(%q) = %d, want %d", tt.name, got, tt.want)
		}
	}
}
