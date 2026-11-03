package agent

import (
	"fmt"
	"path/filepath"

	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	diskWarnThreshold = uint64(2 * 1024 * 1024 * 1024)

	diskCriticalThreshold = uint64(500 * 1024 * 1024)

	diskCheckInterval = 2 * time.Minute
)

type diskSpaceState struct {
	fired      bool
	lastCheck  time.Time
	lastFree   uint64
	lastTotal  uint64
	lastResult string
}

func newDiskSpaceState() *diskSpaceState {
	return &diskSpaceState{}
}

func (d *diskSpaceState) reset() {
	d.fired = false

}

func (d *diskSpaceState) check(workingDir string) string {
	if d.fired {
		return d.lastResult
	}

	if time.Since(d.lastCheck) < diskCheckInterval && d.lastResult != "" {
		d.fired = true
		return d.lastResult
	}

	if workingDir == "" {
		return ""
	}

	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		return ""
	}

	free, total, ok := diskUsage(absDir)
	if !ok {
		debug.Log("disk-space", "statfs failed for %s, skipping check", absDir)
		return ""
	}

	d.lastCheck = time.Now()
	d.lastFree = free
	d.lastTotal = total

	var msg string
	if free < diskCriticalThreshold {
		msg = fmt.Sprintf(
			"[disk-space] CRITICAL: only %s free on workspace volume. "+
				"File writes, builds, and git operations will likely fail. "+
				"Free up space before proceeding (clear caches, remove temp files, prune worktrees).",
			formatDiskSize(free),
		)
	} else if free < diskWarnThreshold {
		pct := float64(free) / float64(total) * 100
		if total == 0 {
			pct = 0
		}
		msg = fmt.Sprintf(
			"[disk-space] Warning: %s free (%.0f%% of volume). "+
				"Large file operations may fail. Consider cleaning up before heavy builds.",
			formatDiskSize(free),
			pct,
		)
	}

	d.lastResult = msg
	d.fired = msg != ""
	return msg
}

func formatDiskSize(bytes uint64) string {
	const (
		kiB = 1024
		miB = 1024 * 1024
		giB = 1024 * 1024 * 1024
	)
	switch {
	case bytes >= giB:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(giB))
	case bytes >= miB:
		return fmt.Sprintf("%.0f MiB", float64(bytes)/float64(miB))
	case bytes >= kiB:
		return fmt.Sprintf("%.0f KiB", float64(bytes)/float64(kiB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func diskUsage(path string) (free, total uint64, ok bool) {
	return diskUsageOS(path)
}
