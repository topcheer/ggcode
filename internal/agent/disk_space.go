package agent

// Disk Space / Resource Monitor
//
// Detects low disk space on the workspace volume before the agent starts working.
// When the workspace is critically low on disk, file operations (write_file,
// edit_file, git operations, build/test commands) will fail silently or with
// cryptic "no space left on device" errors. The agent then wastes iterations
// retrying or misdiagnosing the problem.
//
// Competitor analysis:
//   - Claude Code: no disk space awareness; fails on write errors
//   - Cursor: IDE shows OS-level warnings but agent has no awareness
//   - Devin: no proactive disk monitoring
//   - OpenHands/Cline: no disk awareness; sandbox OOM/disk-full is top crash cause
//   - Aider: git-based, fails on commit when disk is full
//
// ggcode's approach: check free space once at run start. If below warning or
// critical thresholds, inject a concise advisory so the agent can prioritize
// cleanup (clear caches, remove large temp files, prune worktrees) before
// proceeding with the task. Zero LLM cost, fires at most once per run.
//
// Design constraints:
//   - Cross-platform: syscall.Statfs on Unix, windows.GetDiskFreeSpaceEx on Windows
//   - Non-blocking: 50ms timeout via context
//   - Non-fatal: if stat fails (network FS, permission), silently skip
//   - At most one injection per run

import (
	"fmt"
	"path/filepath"

	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// diskWarnThreshold: below this, inject a warning advisory.
	// 2 GiB - enough headroom for most builds but signals the agent should
	// be mindful of large file operations.
	diskWarnThreshold = uint64(2 * 1024 * 1024 * 1024)

	// diskCriticalThreshold: below this, inject a critical advisory.
	// 500 MiB - git operations, builds, and temp files will likely fail.
	diskCriticalThreshold = uint64(500 * 1024 * 1024)

	// diskCheckInterval: minimum time between disk space checks across runs.
	// Avoids repeated syscall.Statfs on every user turn.
	diskCheckInterval = 2 * time.Minute
)

// diskSpaceState tracks whether the disk space advisory has been injected
// and caches the last check to avoid redundant statfs calls.
type diskSpaceState struct {
	fired      bool
	lastCheck  time.Time
	lastFree   uint64
	lastTotal  uint64
	lastResult string // cached message, empty if no issue
}

func newDiskSpaceState() *diskSpaceState {
	return &diskSpaceState{}
}

func (d *diskSpaceState) reset() {
	d.fired = false
	// Keep lastCheck/lastFree to allow cross-run caching within diskCheckInterval
}

// checkDiskSpace inspects the free space on the volume containing the working
// directory. Returns a non-empty advisory message if space is low, or empty
// string if space is adequate or the check failed.
func (d *diskSpaceState) check(workingDir string) string {
	if d.fired {
		return d.lastResult
	}

	// Use cached result if checked recently
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

// formatDiskSize renders a byte count as a human-readable string.
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

// diskUsage returns (free bytes, total bytes, ok) for the filesystem containing path.
// Platform-specific implementations are in disk_space_unix.go and disk_space_windows.go.
func diskUsage(path string) (free, total uint64, ok bool) {
	return diskUsageOS(path)
}
