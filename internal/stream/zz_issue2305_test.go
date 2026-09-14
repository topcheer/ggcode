package stream

import (
	"strings"
	"testing"
)

func validTarget() []StreamTarget {
	return []StreamTarget{{Name: "t", URL: "rtmp://a.example.com/live", Key: "k"}}
}

// #2305: srt:// promised support but failed 100% at runtime (path-append
// key + -f flv). Validate must reject it honestly at config time.
func TestIssue2305SrtRejectedAtConfigTime(t *testing.T) {
	c := &StreamConfig{Width: 1920, Height: 1080, Quality: 26, Targets: []StreamTarget{
		{Name: "s", URL: "srt://host:9000", Key: "k"},
	}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "srt:// targets are not supported") {
		t.Fatalf("srt target must be rejected with the honest message, got %v", err)
	}
}

// rtmp/rtmps stay first-class
func TestIssue2305RtmpRtmpsStillValid(t *testing.T) {
	for _, scheme := range []string{"rtmp://", "rtmps://"} {
		c := &StreamConfig{Width: 1920, Height: 1080, Quality: 26, Targets: []StreamTarget{
			{Name: "t", Enabled: true, URL: scheme + "a.example.com/live", Key: "k"},
		}}
		if err := c.Validate(); err != nil {
			t.Fatalf("%s target must stay valid, got %v", scheme, err)
		}
	}
}

// #2305: odd dimensions must fail at config time, not in ffmpeg
func TestIssue2305OddDimensionsRejected(t *testing.T) {
	c := &StreamConfig{Width: 1281, Height: 720, Targets: validTarget()}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "must be even") {
		t.Fatalf("odd width must be rejected, got %v", err)
	}
	c = &StreamConfig{Width: 1280, Height: 721, Targets: validTarget()}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "must be even") {
		t.Fatalf("odd height must be rejected, got %v", err)
	}
}

// #2305: FullURL trims whitespace, matching Validate's trimmed check
func TestIssue2305FullURLTrimsWhitespace(t *testing.T) {
	tgt := StreamTarget{URL: " rtmp://a.example.com/live/ ", Key: "k"}
	if got := tgt.FullURL(); got != "rtmp://a.example.com/live/k" {
		t.Fatalf("FullURL must trim whitespace before appending the key, got %q", got)
	}
}
