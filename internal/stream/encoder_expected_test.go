package stream

import (
	"testing"
)

func TestEncoderExpectedFrameSize(t *testing.T) {
	tests := []struct {
		width, height int
		expected      int
	}{
		{100, 100, 100 * 100 * 4},
		{640, 480, 640 * 480 * 4},
		{1280, 720, 1280 * 720 * 4},
		{1920, 1080, 1920 * 1080 * 4},
		{1, 1, 4},
	}

	for _, tt := range tests {
		enc := NewEncoder(tt.width, tt.height, 26, 15, "")
		got := enc.ExpectedFrameSize()
		if got != tt.expected {
			t.Errorf("ExpectedFrameSize(%d,%d) = %d, want %d",
				tt.width, tt.height, got, tt.expected)
		}
	}
}
