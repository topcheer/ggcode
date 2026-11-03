package stream

import (
	"strings"
	"testing"
)

func TestIssue1631Case4_URLSchemeValidated(t *testing.T) {
	bad := []string{"youtube.example/live", "http://example/live", "rtmp:/typo"}
	for _, u := range bad {
		c := StreamConfig{
			Width: 1920, Height: 1080, FPS: 30, Quality: 28,
			Targets: []StreamTarget{{Name: "t", URL: u, Key: "k", Enabled: true}},
		}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "rtmp://") {
			t.Errorf("url %q must be rejected with a scheme hint, got %v", u, err)
		}
	}
	good := StreamConfig{
		Width: 1920, Height: 1080, FPS: 30, Quality: 28,
		Targets: []StreamTarget{{Name: "t", URL: "rtmp://a.example/live", Key: "k", Enabled: true}},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("valid rtmp url must pass, got %v", err)
	}
}
