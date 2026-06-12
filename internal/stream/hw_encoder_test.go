package stream

import (
	"testing"
)

func TestDetectHardwareEncoders(t *testing.T) {
	encoders := DetectHardwareEncoders()
	t.Logf("Detected %d hardware encoder(s):", len(encoders))
	for _, e := range encoders {
		t.Logf("  %s (%s) platform=%s", e.Name, e.Description, e.Platform)
	}

	best := BestEncoder("")
	t.Logf("BestEncoder(auto) = %s", best)
	if best == "" {
		t.Error("BestEncoder returned empty string")
	}

	forced := BestEncoder("h264_nvenc")
	t.Logf("BestEncoder(forced nvenc) = %s", forced)
	if forced != "h264_nvenc" {
		t.Errorf("forced = %q, want h264_nvenc", forced)
	}

	sw := BestEncoder("software")
	t.Logf("BestEncoder(software) = %s", sw)
	if sw != "libx264" {
		t.Errorf("software = %q, want libx264", sw)
	}
}

func TestEncoderIsHardware(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"libx264", false},
		{"libx265", false},
		{"h264_videotoolbox", true},
		{"h264_nvenc", true},
		{"h264_qsv", true},
		{"h264_vaapi", true},
		{"h264_amf", true},
	}
	for _, tt := range tests {
		got := EncoderIsHardware(tt.name)
		if got != tt.want {
			t.Errorf("EncoderIsHardware(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestBuildArgsSoftware(t *testing.T) {
	enc := NewEncoder(640, 480, 26, 15, "software")
	args := enc.buildArgs("libx264", false)

	argsStr := ""
	for _, a := range args {
		argsStr += " " + a
	}
	t.Logf("Software args:%s", argsStr)

	// Verify key args
	if !containsArg(args, "-c:v") || !containsArgValue(args, "-c:v", "libx264") {
		t.Error("missing -c:v libx264")
	}
	if !containsArg(args, "-preset") || !containsArgValue(args, "-preset", "fast") {
		t.Error("missing -preset fast")
	}
	if !containsArg(args, "-tune") || !containsArgValue(args, "-tune", "stillimage") {
		t.Error("missing -tune stillimage")
	}
	if !containsArg(args, "-b:v") {
		t.Error("missing -b:v (CBR bitrate)")
	}
}

func TestBuildArgsHardware(t *testing.T) {
	enc := NewEncoder(640, 480, 26, 15, "auto")
	args := enc.buildArgs("h264_videotoolbox", true)

	argsStr := ""
	for _, a := range args {
		argsStr += " " + a
	}
	t.Logf("Hardware args:%s", argsStr)

	// Hardware encoder should NOT have preset/tune/crf
	if containsArg(args, "-preset") {
		t.Error("hardware encoder should not have -preset")
	}
	if containsArg(args, "-tune") {
		t.Error("hardware encoder should not have -tune")
	}
	if containsArg(args, "-crf") {
		t.Error("hardware encoder should not have -crf")
	}
	// But should have bitrate
	if !containsArg(args, "-b:v") {
		t.Error("missing -b:v for hardware encoder")
	}
	if !containsArgValue(args, "-c:v", "h264_videotoolbox") {
		t.Error("missing -c:v h264_videotoolbox")
	}
}

func containsArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func containsArgValue(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}
