package agent

import (
	"testing"
	"time"
)

func TestNewDiskSpaceState(t *testing.T) {
	d := newDiskSpaceState()
	if d == nil {
		t.Fatal("newDiskSpaceState returned nil")
	}
	if d.fired {
		t.Error("new state should not have fired=true")
	}
}

func TestDiskSpaceReset(t *testing.T) {
	d := &diskSpaceState{fired: true, lastResult: "test"}
	d.reset()
	if d.fired {
		t.Error("reset should clear fired flag")
	}

	if d.lastResult != "test" {
		t.Error("reset should preserve lastResult for caching")
	}
}

func TestDiskSpaceCheckEmptyDir(t *testing.T) {
	d := newDiskSpaceState()
	msg := d.check("")
	if msg != "" {
		t.Errorf("check with empty dir should return empty, got: %s", msg)
	}
}

func TestDiskSpaceCheckValidDir(t *testing.T) {
	d := newDiskSpaceState()

	msg := d.check(".")

	_ = msg
}

func TestDiskSpaceCheckFiresOnce(t *testing.T) {
	d := newDiskSpaceState()

	d.fired = true
	d.lastResult = ""
	msg := d.check(".")
	if msg != "" {
		t.Error("check after fire should return cached result (empty)")
	}
}

func TestDiskSpaceCheckCaching(t *testing.T) {
	d := newDiskSpaceState()
	d.lastCheck = time.Now()
	d.lastResult = "cached warning"

	msg := d.check(".")
	if msg != "cached warning" {
		t.Errorf("expected cached result, got: %s", msg)
	}
}

func TestFormatDiskSize(t *testing.T) {
	tests := []struct {
		bytes uint64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1 KiB"},
		{1536, "2 KiB"},
		{1024 * 1024, "1 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
		{2 * 1024 * 1024 * 1024, "2.0 GiB"},
		{500 * 1024 * 1024, "500 MiB"},
	}
	for _, tt := range tests {
		got := formatDiskSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatDiskSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestDiskUsageValidPath(t *testing.T) {
	free, total, ok := diskUsageOS(".")
	if !ok {
		t.Skip("diskUsage not available on this platform or path invalid")
	}
	if free == 0 {

	}
	if total == 0 {
		t.Error("total disk size should be non-zero")
	}
}

func TestDiskUsageInvalidPath(t *testing.T) {
	_, _, ok := diskUsage("/nonexistent/path/that/should/not/exist")

	_ = ok
}
