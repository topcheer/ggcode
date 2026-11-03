package stream

import (
	"fmt"
	"os"
	"strings"
)

type StreamConfig struct {
	HardwareEncoder string `yaml:"hardware_encoder,omitempty" json:"hardware_encoder,omitempty"`
	FPS             int    `yaml:"fps,omitempty" json:"fps,omitempty"`

	Width int `yaml:"width,omitempty" json:"width,omitempty"`

	Height int `yaml:"height,omitempty" json:"height,omitempty"`

	Quality int `yaml:"quality,omitempty" json:"quality,omitempty"`

	FontSize int `yaml:"font_size,omitempty" json:"font_size,omitempty"`

	FontPath string `yaml:"font_path,omitempty" json:"font_path,omitempty"`

	Targets []StreamTarget `yaml:"targets,omitempty" json:"targets,omitempty"`
}

type StreamTarget struct {
	Name string `yaml:"name" json:"name"`

	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	URL string `yaml:"url" json:"url"`

	Key string `yaml:"key" json:"key"`
}

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

func (c *StreamConfig) Validate() error {
	if c.Width < 160 || c.Width > 3840 {
		return fmt.Errorf("stream: width must be between 160 and 3840, got %d", c.Width)
	}
	if c.Height < 120 || c.Height > 2160 {
		return fmt.Errorf("stream: height must be between 120 and 2160, got %d", c.Height)
	}

	if c.Width%2 != 0 || c.Height%2 != 0 {
		return fmt.Errorf("stream: width and height must be even (yuv420p requires it), got %dx%d", c.Width, c.Height)
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

		if u := strings.TrimSpace(t.URL); !strings.HasPrefix(u, "rtmp://") && !strings.HasPrefix(u, "rtmps://") {
			if strings.HasPrefix(u, "srt://") {
				return fmt.Errorf("stream: target[%d] (%s): srt:// targets are not supported yet (use rtmp:// or rtmps://)", i, t.Name)
			}
			return fmt.Errorf("stream: target[%d] (%s): url must start with rtmp:// or rtmps:// (got %q)", i, t.Name, u)
		}
		if strings.TrimSpace(t.Key) == "" {
			return fmt.Errorf("stream: target[%d] (%s): key is required", i, t.Name)
		}
		if names[t.Name] {
			return fmt.Errorf("stream: duplicate target name %q", t.Name)
		}
		names[t.Name] = true
	}

	if len(c.Targets) > 0 {
		anyEnabled := false
		for _, t := range c.Targets {
			if t.Enabled {
				anyEnabled = true
				break
			}
		}
		if !anyEnabled {
			return fmt.Errorf("stream: %d target(s) configured but none enabled: set 'enabled: true' on at least one target", len(c.Targets))
		}
	}
	return nil
}

func (c *StreamConfig) ExpandEnv() {
	for i := range c.Targets {
		c.Targets[i].Key = expandEnvVar(c.Targets[i].Key)
	}
}

func (t *StreamTarget) FullURL() string {
	key := expandEnvVar(t.Key)
	return strings.TrimRight(strings.TrimSpace(t.URL), "/") + "/" + key
}

func expandEnvVar(s string) string {
	return os.ExpandEnv(s)
}
