package stream

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

type HardwareEncoder struct {
	Name        string
	Description string
	Platform    string
}

var (
	hwEncoders     []HardwareEncoder
	hwEncodersOnce sync.Once
)

var preferredEncoderOrder = []HardwareEncoder{
	{Name: "h264_videotoolbox", Description: "Apple VideoToolbox", Platform: "darwin"},
	{Name: "h264_mf", Description: "Windows Media Foundation", Platform: "windows"},
	{Name: "h264_nvenc", Description: "NVIDIA NVENC", Platform: ""},
	{Name: "h264_qsv", Description: "Intel Quick Sync", Platform: ""},
	{Name: "h264_vaapi", Description: "VA-API (Linux AMD/Intel)", Platform: "linux"},
	{Name: "h264_amf", Description: "AMD AMF", Platform: "windows"},
}

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

					break
				}
			}
		}
		if len(hwEncoders) == 0 {

		}
	})
	return hwEncoders
}

func BestEncoder(forceEncoder string) string {
	switch forceEncoder {
	case "software", "libx264":

		return "libx264"
	case "", "auto":
		detected := DetectHardwareEncoders()
		if len(detected) > 0 {
			choice := detected[0].Name

			return choice
		}
		return "libx264"
	default:

		return forceEncoder
	}
}

func EncoderIsHardware(name string) bool {
	softwareEncoders := map[string]bool{
		"libx264": true, "libx265": true, "libvpx-vp9": true,
	}
	return !softwareEncoders[name]
}

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

		line = strings.TrimSpace(line)
		if len(line) < 10 {
			continue
		}

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
