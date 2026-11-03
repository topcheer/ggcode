package stream

import "testing"

func TestIssue2274RtmpsPassesValidate(t *testing.T) {
	c := &StreamConfig{Targets: []StreamTarget{{
		Name:    "yt",
		Enabled: true,
		URL:     "rtmps://a.rtmp.youtube.com/live2",
		Key:     "k-1234",
	}}}
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		t.Errorf("rtmps target must validate: %v", err)
	}
}

func TestIssue2274AllPresetsValidate(t *testing.T) {
	for _, p := range Presets {
		c := &StreamConfig{Targets: []StreamTarget{{
			Name:    p.Name,
			Enabled: true,
			URL:     p.URL,
			Key:     "test-key",
		}}}
		c.ApplyDefaults()
		if err := c.Validate(); err != nil {
			t.Errorf("preset %s (%s) must validate, got: %v", p.Name, p.URL, err)
		}
	}
}

func TestIssue2274BadSchemeStillRejected(t *testing.T) {
	c := &StreamConfig{Targets: []StreamTarget{{Name: "bad", Enabled: true, URL: "http://example.com/live", Key: "k"}}}
	c.ApplyDefaults()
	if err := c.Validate(); err == nil {
		t.Error("non-stream scheme must still be rejected")
	}
}
