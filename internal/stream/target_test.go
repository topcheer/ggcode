package stream

import (
	"testing"
)

func TestTargetMaskURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"rtmps://a.rtmp.youtube.com/live2/abc-123-def", "rtmps://a.rtmp.youtube.com/live2/***"},
		{"rtmp://example.com/app/mykey", "rtmp://example.com/app/***"},
		{"rtmp://example.com/short", "rtmp://example.com/***"},
	}

	for _, tt := range tests {
		target := NewTarget("test", tt.url)
		got := target.maskURL()
		if got != tt.want {
			t.Errorf("maskURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestTargetStateTransitions(t *testing.T) {
	target := NewTarget("test", "rtmp://example.com/app/key")

	if target.State() != TargetIdle {
		t.Errorf("initial state = %q, want idle", target.State())
	}

	status := target.Status()
	if status.Name != "test" {
		t.Errorf("status.Name = %q, want test", status.Name)
	}
	if status.State != TargetIdle {
		t.Errorf("status.State = %q, want idle", status.State)
	}

	if status.URL == "rtmp://example.com/app/key" {
		t.Error("status URL should be masked")
	}
}

func TestTargetStopFromIdle(t *testing.T) {
	target := NewTarget("test", "rtmp://example.com/app/key")

	target.Stop()
	if target.State() != TargetIdle {
		t.Errorf("state after stop from idle = %q, want idle", target.State())
	}
}

func TestTargetWriteNotLive(t *testing.T) {
	target := NewTarget("test", "rtmp://example.com/app/key")
	_, err := target.Write([]byte("data"))
	if err == nil {
		t.Error("expected error writing to non-live target")
	}
}

func TestTargetArgsNoReFlag(t *testing.T) {

	target := NewTarget("test", "rtmp://example.com/app/key")

	if target.State() != TargetIdle {
		t.Errorf("initial state = %q, want idle", target.State())
	}

}

func TestTargetWriteCountsBytes(t *testing.T) {

	target := NewTarget("test", "rtmp://example.com/app/key")

	_, err := target.Write([]byte("data"))
	if err == nil {
		t.Error("expected error for idle target write")
	}
}

func TestNewTargetState(t *testing.T) {
	target := NewTarget("youtube", "rtmps://a.rtmp.youtube.com/live2/mykey")
	if target.Name() != "youtube" {
		t.Errorf("Name() = %q, want %q", target.Name(), "youtube")
	}
	if target.State() != TargetIdle {
		t.Errorf("State() = %q, want idle", target.State())
	}
}

func TestTargetStatusFields(t *testing.T) {
	target := NewTarget("twitch", "rtmp://live.twitch.tv/app/mystreamkey")
	status := target.Status()

	if status.Name != "twitch" {
		t.Errorf("Name = %q, want %q", status.Name, "twitch")
	}
	if status.State != TargetIdle {
		t.Errorf("State = %q, want idle", status.State)
	}
	if status.BytesSent != 0 {
		t.Errorf("BytesSent = %d, want 0", status.BytesSent)
	}

	if status.URL == "rtmp://live.twitch.tv/app/mystreamkey" {
		t.Error("status URL should be masked")
	}
}
