package stream

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// FFmpegCheck holds the result of an ffmpeg availability check.
type FFmpegCheck struct {
	Available bool
	Path      string
	Version   string
	Major     int
	Minor     int
	Error     string
}

// CheckFFmpeg verifies that ffmpeg is installed and meets the minimum version requirement.
func CheckFFmpeg() FFmpegCheck {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return FFmpegCheck{
			Error: "ffmpeg is not installed or not in PATH.\n\n" + ffmpegInstallHint(),
		}
	}

	out, err := exec.Command(path, "-version").Output()
	if err != nil {
		return FFmpegCheck{
			Path:  path,
			Error: fmt.Sprintf("ffmpeg found at %s but failed to get version: %v", path, err),
		}
	}

	// Parse version from output like "ffmpeg version 6.1.2" or "ffmpeg version 8.1"
	line := strings.Split(string(out), "\n")[0]
	re := regexp.MustCompile(`ffmpeg version (\d+)\.(\d+)`)
	matches := re.FindStringSubmatch(line)
	if matches == nil {
		return FFmpegCheck{
			Path:    path,
			Version: strings.TrimPrefix(line, "ffmpeg version "),
			Error:   fmt.Sprintf("could not parse ffmpeg version from: %s", line),
		}
	}

	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	version := matches[1] + "." + matches[2]

	if major < 4 {
		return FFmpegCheck{
			Path:    path,
			Version: version,
			Major:   major,
			Minor:   minor,
			Error:   fmt.Sprintf("ffmpeg version %s is too old (need >= 4.0). Current: %s", version, line),
		}
	}

	return FFmpegCheck{
		Available: true,
		Path:      path,
		Version:   version,
		Major:     major,
		Minor:     minor,
	}
}

// ffmpegInstallHint returns platform-specific installation instructions.
func ffmpegInstallHint() string {
	return strings.Join([]string{
		"Install ffmpeg:",
		"",
		"  macOS:   brew install ffmpeg",
		"  Ubuntu:  sudo apt install ffmpeg",
		"  Fedora:  sudo dnf install ffmpeg",
		"  Arch:    sudo pacman -S ffmpeg",
		"  Windows: winget install ffmpeg",
		"           or download from https://ffmpeg.org/download.html",
		"",
		"Minimum version: ffmpeg >= 4.0",
	}, "\n")
}
