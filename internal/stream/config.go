package stream

import (
	"fmt"
	"os"
	"strings"
)

// StreamConfig holds live-streaming configuration.
type StreamConfig struct {
	// HardwareEncoder controls which H.264 encoder to use.
	// "auto" — detect best available (default)
	// "software" — force libx264
	// "h264_videotoolbox", "h264_nvenc", "h264_qsv", "h264_vaapi" — force specific
	HardwareEncoder string `yaml:"hardware_encoder,omitempty" json:"hardware_encoder,omitempty"`
	FPS             int    `yaml:"fps,omitempty" json:"fps,omitempty"`
	// Width of the output video in pixels (default 1280).
	Width int `yaml:"width,omitempty" json:"width,omitempty"`
	// Height of the output video in pixels (default 720).
	Height int `yaml:"height,omitempty" json:"height,omitempty"`
	// Quality is the H.264 QP value (default 26). Lower = better quality, higher bitrate.
	Quality int `yaml:"quality,omitempty" json:"quality,omitempty"`
	// FontSize in points for the terminal font rendering (default 14).
	FontSize int `yaml:"font_size,omitempty" json:"font_size,omitempty"`
	// FontPath is an optional path to a custom TTF font file.
	FontPath string `yaml:"font_path,omitempty" json:"font_path,omitempty"`
	// Targets is the list of streaming platform targets.
	Targets []StreamTarget `yaml:"targets,omitempty" json:"targets,omitempty"`
}

// StreamTarget represents a single streaming platform destination.
type StreamTarget struct {
	// Name is a human-readable identifier (e.g., "youtube", "bilibili").
	Name string `yaml:"name" json:"name"`
	// Enabled controls whether this target is active.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// URL is the RTMP/RTMPS endpoint (without the stream key).
	// Examples:
	//   - YouTube: rtmps://a.rtmp.youtube.com/live2
	//   - Bilibili: rtmp://live-push.bilivideo.com/live-bvc
	//   - Twitch:  rtmps://live.twitch.tv/app
	URL string `yaml:"url" json:"url"`
	// Key is the stream key. Supports ${ENV_VAR} expansion.
	Key string `yaml:"key" json:"key"`
}

// ApplyDefaults fills in zero-valued fields with sensible defaults.
func (c *StreamConfig) ApplyDefaults() {
	if c.FPS <= 0 {
		c.FPS = 15
	}
	if c.FPS > 60 {
		c.FPS = 60
	}
	if c.Width <= 0 {
		c.Width = 1280
	}
	if c.Height <= 0 {
		c.Height = 720
	}
	if c.Quality <= 0 {
		c.Quality = 26
	}
	if c.FontSize <= 0 {
		c.FontSize = 14
	}
}

// Validate checks the configuration for errors.
func (c *StreamConfig) Validate() error {
	if c.Width < 160 || c.Width > 3840 {
		return fmt.Errorf("stream: width must be between 160 and 3840, got %d", c.Width)
	}
	if c.Height < 120 || c.Height > 2160 {
		return fmt.Errorf("stream: height must be between 120 and 2160, got %d", c.Height)
	}
	if c.Quality < 10 || c.Quality > 51 {
		return fmt.Errorf("stream: quality (QP) must be between 10 and 51, got %d", c.Quality)
	}
	names := make(map[string]bool)
	for i, t := range c.Targets {
		if strings.TrimSpace(t.Name) == "" {
			return fmt.Errorf("stream: target[%d]: name is required", i)
		}
		if strings.TrimSpace(t.URL) == "" {
			return fmt.Errorf("stream: target[%d] (%s): url is required", i, t.Name)
		}
		if strings.TrimSpace(t.Key) == "" {
			return fmt.Errorf("stream: target[%d] (%s): key is required", i, t.Name)
		}
		if names[t.Name] {
			return fmt.Errorf("stream: duplicate target name %q", t.Name)
		}
		names[t.Name] = true
	}
	return nil
}

// ExpandEnv expands ${ENV_VAR} references in all target stream keys.
func (c *StreamConfig) ExpandEnv() {
	for i := range c.Targets {
		c.Targets[i].Key = expandEnvVar(c.Targets[i].Key)
	}
}

// FullURL returns the complete RTMP URL with stream key appended.
func (t *StreamTarget) FullURL() string {
	key := expandEnvVar(t.Key)
	return strings.TrimRight(t.URL, "/") + "/" + key
}

// expandEnvVar expands ${VAR} and $VAR in the input string using os.Getenv.
func expandEnvVar(s string) string {
	return os.ExpandEnv(s)
}
