package stream

import (
	"os"
	"testing"
)

func TestStreamConfigApplyDefaults(t *testing.T) {
	cfg := StreamConfig{}
	cfg.ApplyDefaults()

	if cfg.FPS != 15 {
		t.Errorf("default FPS = %d, want 15", cfg.FPS)
	}
	if cfg.Width != 1280 {
		t.Errorf("default Width = %d, want 1280", cfg.Width)
	}
	if cfg.Height != 720 {
		t.Errorf("default Height = %d, want 720", cfg.Height)
	}
	if cfg.Quality != 26 {
		t.Errorf("default Quality = %d, want 26", cfg.Quality)
	}
	if cfg.FontSize != 14 {
		t.Errorf("default FontSize = %d, want 14", cfg.FontSize)
	}
}

func TestStreamConfigApplyDefaultsCaps(t *testing.T) {
	cfg := StreamConfig{FPS: 120}
	cfg.ApplyDefaults()
	if cfg.FPS != 60 {
		t.Errorf("FPS should cap at 60, got %d", cfg.FPS)
	}
}

func TestStreamConfigApplyDefaultsPreservesValues(t *testing.T) {
	cfg := StreamConfig{FPS: 30, Width: 1920, Height: 1080, Quality: 20, FontSize: 18}
	cfg.ApplyDefaults()
	if cfg.FPS != 30 {
		t.Errorf("FPS should be preserved, got %d", cfg.FPS)
	}
	if cfg.Width != 1920 {
		t.Errorf("Width should be preserved, got %d", cfg.Width)
	}
	if cfg.Height != 1080 {
		t.Errorf("Height should be preserved, got %d", cfg.Height)
	}
}

func TestStreamConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*StreamConfig)
		wantErr string
	}{
		{
			name:   "valid defaults",
			modify: func(c *StreamConfig) {},
		},
		{
			name:    "width too small",
			modify:  func(c *StreamConfig) { c.Width = 100 },
			wantErr: "width must be between 160 and 3840",
		},
		{
			name:    "width too large",
			modify:  func(c *StreamConfig) { c.Width = 4000 },
			wantErr: "width must be between 160 and 3840",
		},
		{
			name:    "height too small",
			modify:  func(c *StreamConfig) { c.Height = 100 },
			wantErr: "height must be between 120 and 2160",
		},
		{
			name:    "quality too low",
			modify:  func(c *StreamConfig) { c.Quality = 5 },
			wantErr: "quality (QP) must be between 10 and 51",
		},
		{
			name:    "quality too high",
			modify:  func(c *StreamConfig) { c.Quality = 60 },
			wantErr: "quality (QP) must be between 10 and 51",
		},
		{
			name: "target missing name",
			modify: func(c *StreamConfig) {
				c.Targets = []StreamTarget{{Name: "", URL: "rtmp://example.com", Key: "abc"}}
			},
			wantErr: "name is required",
		},
		{
			name: "target missing url",
			modify: func(c *StreamConfig) {
				c.Targets = []StreamTarget{{Name: "test", URL: "", Key: "abc"}}
			},
			wantErr: "url is required",
		},
		{
			name: "target missing key",
			modify: func(c *StreamConfig) {
				c.Targets = []StreamTarget{{Name: "test", URL: "rtmp://example.com", Key: ""}}
			},
			wantErr: "key is required",
		},
		{
			name: "duplicate target name",
			modify: func(c *StreamConfig) {
				c.Targets = []StreamTarget{
					{Name: "youtube", URL: "rtmp://a.com", Key: "k1"},
					{Name: "youtube", URL: "rtmp://b.com", Key: "k2"},
				}
			},
			wantErr: "duplicate target name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := StreamConfig{}
			cfg.ApplyDefaults()
			tt.modify(&cfg)

			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q should contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestStreamConfigExpandEnv(t *testing.T) {
	os.Setenv("TEST_STREAM_KEY_123", "my-secret-key")
	defer os.Unsetenv("TEST_STREAM_KEY_123")

	cfg := StreamConfig{
		Targets: []StreamTarget{
			{Name: "test", URL: "rtmp://example.com", Key: "${TEST_STREAM_KEY_123}"},
			{Name: "plain", URL: "rtmp://example.com", Key: "hardcoded-key"},
		},
	}
	cfg.ExpandEnv()

	if cfg.Targets[0].Key != "my-secret-key" {
		t.Errorf("env expansion failed: got %q", cfg.Targets[0].Key)
	}
	if cfg.Targets[1].Key != "hardcoded-key" {
		t.Errorf("plain key should not change: got %q", cfg.Targets[1].Key)
	}
}

func TestStreamTargetFullURL(t *testing.T) {
	os.Setenv("TEST_FULL_URL_KEY", "abc-123")
	defer os.Unsetenv("TEST_FULL_URL_KEY")

	tests := []struct {
		url, key, want string
	}{
		{"rtmps://a.rtmp.youtube.com/live2", "${TEST_FULL_URL_KEY}", "rtmps://a.rtmp.youtube.com/live2/abc-123"},
		{"rtmp://example.com/app/", "mykey", "rtmp://example.com/app/mykey"},
		{"rtmp://example.com/app", "mykey", "rtmp://example.com/app/mykey"},
	}

	for _, tt := range tests {
		target := StreamTarget{URL: tt.url, Key: tt.key}
		got := target.FullURL()
		if got != tt.want {
			t.Errorf("FullURL(%q, %q) = %q, want %q", tt.url, tt.key, got, tt.want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && containsStr(s, sub)))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
