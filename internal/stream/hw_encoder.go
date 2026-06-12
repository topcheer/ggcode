package stream

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// HardwareEncoder represents a detected hardware encoder.
type HardwareEncoder struct {
	Name        string // e.g., "h264_videotoolbox"
	Description string // e.g., "VideoToolbox H.264"
	Platform    string // e.g., "darwin", "linux"
}

var (
	hwEncoders     []HardwareEncoder
	hwEncodersOnce sync.Once
)

// preferredEncoderOrder defines the priority order for hardware encoders.
var preferredEncoderOrder = []HardwareEncoder{
	{Name: "h264_videotoolbox", Description: "Apple VideoToolbox", Platform: "darwin"},
	{Name: "h264_mf", Description: "Windows Media Foundation", Platform: "windows"},
	{Name: "h264_nvenc", Description: "NVIDIA NVENC", Platform: ""},
	{Name: "h264_qsv", Description: "Intel Quick Sync", Platform: ""},
	{Name: "h264_vaapi", Description: "VA-API (Linux AMD/Intel)", Platform: "linux"},
	{Name: "h264_amf", Description: "AMD AMF", Platform: "windows"},
}

// DetectHardwareEncoders probes FFmpeg for available hardware H.264 encoders.
// Results are cached after first call.
func DetectHardwareEncoders() []HardwareEncoder {
	hwEncodersOnce.Do(func() {
		encoders := probeFFmpegEncoders()
		for _, pref := range preferredEncoderOrder {
			if pref.Platform != "" && pref.Platform != runtime.GOOS {
				continue
			}
			for _, enc := range encoders {
				if enc == pref.Name {
					hwEncoders = append(hwEncoders, pref)
					debug.Log("stream", "hardware encoder detected: %s (%s)", pref.Name, pref.Description)
					break
				}
			}
		}
		if len(hwEncoders) == 0 {
			debug.Log("stream", "no hardware encoders found, will use libx264")
		}
	})
	return hwEncoders
}

// BestEncoder selects the optimal encoder name.
// If forceEncoder is non-empty, it's returned directly (user override).
// Otherwise, picks the first available hardware encoder, or "libx264" as fallback.
func BestEncoder(forceEncoder string) string {
	switch forceEncoder {
	case "software", "libx264":
		debug.Log("stream", "using software encoder: libx264")
		return "libx264"
	case "", "auto":
		detected := DetectHardwareEncoders()
		if len(detected) > 0 {
			choice := detected[0].Name
			debug.Log("stream", "auto-selected hardware encoder: %s", choice)
			return choice
		}
		return "libx264"
	default:
		debug.Log("stream", "using forced encoder: %s", forceEncoder)
		return forceEncoder
	}
}

// EncoderIsHardware reports whether the given encoder name is a hardware encoder.
func EncoderIsHardware(name string) bool {
	softwareEncoders := map[string]bool{
		"libx264": true, "libx265": true, "libvpx-vp9": true,
	}
	return !softwareEncoders[name]
}

// probeFFmpegEncoders runs `ffmpeg -encoders` and extracts H.264 encoder names.
func probeFFmpegEncoders() []string {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil
	}

	cmd := exec.Command(path, "-hide_banner", "-encoders")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	var encoders []string
	for _, line := range strings.Split(string(out), "\n") {
		// Lines look like: " V..... h264_videotoolbox  VideoToolbox H.264 encoder"
		line = strings.TrimSpace(line)
		if len(line) < 10 {
			continue
		}
		// Check it's a video encoder line
		if line[0] != ' ' && line[0] != 'V' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[1]
		if strings.HasPrefix(name, "h264_") {
			encoders = append(encoders, name)
		}
	}
	return encoders
}
