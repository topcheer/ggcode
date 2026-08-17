package daemon

import (
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// osStartProcess is indirected for tests so failure paths (PID file
// cleanup on fork failure, #552-C) can be exercised without a real fork.
var osStartProcess = os.StartProcess

// testProcessCmdline is a test hook for mocking processCmdline. It shadows
// the platform-specific implementation when non-nil. Set via tests only.
var testProcessCmdline func(int) string

// DaemonInfo holds metadata about a running daemon process.
type DaemonInfo struct {
	PID        int       `json:"pid"`
	SessionID  string    `json:"session_id"`
	WorkingDir string    `json:"working_dir"`
	StartedAt  time.Time `json:"started_at"`
}

// daemonDir returns ~/.ggcode/daemon/, creating it if needed.
func daemonDir() (string, error) {
	dir := filepath.Join(config.ConfigDir(), "daemon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating daemon dir: %w", err)
	}
	return dir, nil
}

// workDirHash returns a short hex hash for the working directory path.
func workDirHash(workingDir string) string {
	h := md5.Sum([]byte(workingDir))
	return fmt.Sprintf("%x", h)[:12]
}

// PIDFilePath returns the PID file path for a given working directory.
func PIDFilePath(workingDir string) (string, error) {
	dir, err := daemonDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workDirHash(workingDir)+".pid"), nil
}

// LogFilePath returns the log file path for a given working directory.
func LogFilePath(workingDir string) (string, error) {
	dir, err := daemonDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workDirHash(workingDir)+".log"), nil
}

// WritePIDFile writes a PID file with daemon metadata, acquiring an
// exclusive flock to prevent concurrent forks from claiming the same slot
// (#574 Bug D). The lock is held for the lifetime of the file.
func WritePIDFile(path string, pid int, sessionID, workingDir string) error {
	// Open with O_CREATE|O_RDWR; flock provides the atomicity guarantee.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}

	// Non-blocking exclusive lock: returns EAGAIN if another process holds it.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return fmt.Errorf("daemon slot already locked (concurrent fork): %w", err)
	}

	info := DaemonInfo{
		PID:        pid,
		SessionID:  sessionID,
		WorkingDir: workingDir,
		StartedAt:  time.Now(),
	}
	data, err := json.Marshal(info)
	if err != nil {
		f.Close()
		return err
	}

	// Write at offset 0 and truncate; the file is already locked.
	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}

	// Keep the file open and locked for the daemon's lifetime. The OS will
	// release the lock when the process exits (or when we explicitly Close in
	// cleanup paths). Caller is responsible for closing the file when the
	// daemon shuts down.
	return nil
}

// RemovePIDFile deletes the PID file.
func RemovePIDFile(path string) error {
	return os.Remove(path)
}

// ReadPIDFile reads daemon info from a PID file.
func ReadPIDFile(path string) (*DaemonInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info DaemonInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// CheckExistingDaemon checks if a daemon is already running for the given working directory.
// Returns the PID if a running daemon is found, or 0 if none.
func CheckExistingDaemon(workingDir string) (int, error) {
	pidPath, err := PIDFilePath(workingDir)
	if err != nil {
		return 0, err
	}
	info, err := ReadPIDFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
			// #431: a CORRUPT PID file (partial write, disk fault) would linger
			// forever; remove it so the next fork starts clean.
			_ = os.Remove(pidPath)
			return 0, nil
		}
		// #520: any other read error (EACCES on a root-owned 0600 file, NFS
		// EIO, ...) is NOT evidence the daemon is gone. Deleting the PID file
		// here lets the caller fork a second daemon (log interleaving, relay
		// contention). Propagate the error and let the caller decide.
		debug.Log("daemon", "PID file %s unreadable (transient/permission); NOT removing: %v", pidPath, err)
		return 0, fmt.Errorf("reading PID file %s: %w", pidPath, err)
	}
	// Check if process is still alive
	proc, err := os.FindProcess(info.PID)
	if err != nil {
		return 0, nil
	}
	// Send signal 0 to check existence
	if err := checkProcessAlive(proc); err != nil {
		// Process doesn't exist, clean up stale PID file
		_ = os.Remove(pidPath)
		return 0, nil
	}
	// Signal-0 only proves SOME process owns the PID now. After a daemon
	// crash (SIGKILL/panic/power loss) the PID file survives and the OS may
	// later hand the PID to an unrelated process — which would then block
	// daemon startup forever with a false "already running". Verify the
	// process identity before trusting the PID file (#412).
	if !daemonIdentityMatches(info.PID) {
		debug.Log("daemon", "PID %d alive but not a ggcode daemon (recycled PID); cleaning stale PID file %s", info.PID, pidPath)
		_ = os.Remove(pidPath)
		return 0, nil
	}
	return info.PID, nil
}

// daemonIdentityMatches reports whether the process at pid looks like a
// ggcode daemon: its command line carries the hidden --__daemonized flag
// set by ForkIntoBackground (argv[0] is rewritten to "ggcode[dirname]").
//
// When the command line cannot be inspected (unsupported platform,
// permission denied), we do NOT blindly return true. Instead, we check:
//  1. Whether the process is still alive (signal 0)
//  2. Whether the PID file is stale (older than 24h)
//
// This allows self-healing when cmdline is unavailable but the daemon
// has clearly been gone for a long time (#574 Bug C). PID reuse concerns
// (#412/#431) are mitigated by the 24h expiry threshold and the fact that
// we only clean up when the process is actually dead.
func daemonIdentityMatches(pid int) bool {
	var cmdline string
	if testProcessCmdline != nil {
		cmdline = testProcessCmdline(pid)
	} else {
		cmdline = processCmdline(pid)
	}
	if cmdline != "" {
		// Normal path: cmdline available, verify daemon markers.
		if strings.Contains(cmdline, "--__daemonized") || strings.Contains(cmdline, "ggcode[") {
			return true
		}
		// #431: on Windows processCmdline returns only the executable IMAGE
		// NAME (e.g. "ggcode.exe") from a toolhelp32 snapshot — no argv flags.
		// Accept the daemon binary name so the real daemon is not mistaken for
		// a recycled PID.
		base := strings.ToLower(strings.TrimSpace(cmdline))
		if strings.HasSuffix(base, ".exe") {
			base = strings.TrimSuffix(base, ".exe")
		}
		return base == "ggcode" || strings.HasPrefix(base, "ggcode-")
	}

	// Cmdline unavailable: fall back to aliveness + mtime expiry check.
	// First verify the process is actually alive.
	proc, err := os.FindProcess(pid)
	if err != nil {
		// PID not found: process doesn't exist.
		debug.Log("daemon", "daemonIdentityMatches: PID %d not found (os.FindProcess error)", pid)
		return false
	}
	if err := checkProcessAlive(proc); err != nil {
		// Signal 0 failed: process is gone.
		debug.Log("daemon", "daemonIdentityMatches: PID %d signal-0 check failed (process dead)", pid)
		return false
	}

	// Process is alive but cmdline is unavailable. Check mtime to detect stale
	// PID files from crashed daemons. If the file is older than 24h, assume
	// the PID has been recycled and clean it up. This is a safety valve for
	// the rare case where cmdline inspection permanently fails.
	pidPath, err := PIDFilePath(os.Getenv("PWD"))
	if err != nil {
		// Can't resolve PID file path; conservative: assume it's a daemon.
		debug.Log("daemon", "daemonIdentityMatches: cannot resolve PID file path for mtime check, assuming alive")
		return true
	}
	info, err := os.Stat(pidPath)
	if err != nil {
		// PID file doesn't exist; this shouldn't happen here (we're called after
		// ReadPIDFile succeeded), but treat conservatively.
		debug.Log("daemon", "daemonIdentityMatches: PID file stat error, assuming alive: %v", err)
		return true
	}
	age := time.Since(info.ModTime())
	if age > 24*time.Hour {
		debug.Log("daemon", "daemonIdentityMatches: PID %d alive but PID file is %v old; assuming recycled PID and stale", pid, age.Round(time.Hour))
		return false
	}

	// Process is alive and PID file is recent (<24h). Conservatively accept it.
	// PID reuse risk exists but is mitigated by the 24h threshold.
	debug.Log("daemon", "daemonIdentityMatches: PID %d alive, PID file age %v, assuming daemon (cmdline unavailable)", pid, age.Round(time.Minute))
	return true
}

// EnsureDaemonSlot verifies that no live daemon already owns the given
// working directory before a new fork is attempted. It returns a non-nil
// error when a daemon is already running (pid reported) or when the PID
// file cannot be read to rule one out (#552-A: the double-start guard
// existed but was never wired into the fork entry points).
func EnsureDaemonSlot(workingDir string) error {
	pid, err := CheckExistingDaemon(workingDir)
	if err != nil {
		return fmt.Errorf("checking existing daemon: %w", err)
	}
	if pid != 0 {
		return fmt.Errorf("daemon already running for %s (pid %d)", workingDir, pid)
	}
	return nil
}

// backgroundStdin returns an idle stdin for the daemonized child: /dev/null.
// Inheriting the parent's tty stdin (#552-B) caused two failures:
// term.MakeRaw on the user's terminal broke echo, and an SSH disconnect
// delivered EOF on stdin, making the "background" daemon exit.
func backgroundStdin() (*os.File, error) {
	return os.OpenFile(os.DevNull, os.O_RDWR, 0)
}

// ForkIntoBackground re-execs the current binary as a background daemon.
// The child process argv[0] is set to "ggcode[dirname]".
// stdout/stderr are redirected to a log file.
// Returns the child PID; the caller (parent) should os.Exit(0).
func ForkIntoBackground(cfgFile, workingDir, sessionID string, extraArgs ...string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("finding executable: %w", err)
	}

	dirname := filepath.Base(workingDir)
	displayName := "ggcode[" + dirname + "]"

	// Build args: original args + hidden flag + extra args
	args := make([]string, len(os.Args), len(os.Args)+1+len(extraArgs))
	copy(args, os.Args)
	args = append(args, "--__daemonized")
	args = append(args, extraArgs...)
	// Set argv[0] to display name
	args[0] = displayName

	// Open log file.
	// NOTE: Log rotation is not supported by this simple O_APPEND implementation.
	// If external log rotation is used, the daemon will continue writing to the old
	// inode (the old file) because the fd is never re-opened after rotation.
	// To properly support rotation, the daemon would need to track the current
	// log file inode and reopen on change, or use a signal-based reopen mechanism.
	// This limitation is documented in issue #574 (Bug E).
	logPath, err := LogFilePath(workingDir)
	if err != nil {
		return 0, fmt.Errorf("resolving log path: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("opening log file: %w", err)
	}

	// Write PID file path
	pidPath, err := PIDFilePath(workingDir)
	if err != nil {
		return 0, fmt.Errorf("resolving PID path: %w", err)
	}

	// #552-B: stdin must be /dev/null, never the parent's tty. The old
	// comment claimed "will be /dev/null in background" but the code
	// inherited os.Stdin.
	stdin, err := backgroundStdin()
	if err != nil {
		logFile.Close()
		return 0, fmt.Errorf("opening %s for daemon stdin: %w", os.DevNull, err)
	}

	procAttr := &os.ProcAttr{
		Dir: workingDir,
		Env: os.Environ(),
		Files: []*os.File{
			stdin,   // stdin → /dev/null (#552-B)
			logFile, // stdout → log
			logFile, // stderr → log
		},
		Sys: newBackgroundSysProcAttr(),
	}

	process, err := osStartProcess(executable, args, procAttr)
	stdin.Close()
	logFile.Close()
	if err != nil {
		// #552-C: a failed fork must not leave a stale PID file behind —
		// remove any pre-existing file so the next start begins clean.
		_ = os.Remove(pidPath)
		return 0, fmt.Errorf("starting background process: %w", err)
	}

	// Write PID file
	if err := WritePIDFile(pidPath, process.Pid, sessionID, workingDir); err != nil {
		// Non-fatal: the daemon is running but we couldn't record it
		debug.Log("daemon", "could not write PID file: %v", err)
	}

	return process.Pid, nil
}

// CleanupDaemon removes the PID file for the given working directory, but
// ONLY when it is owned by the current process (#552-E). A foreground
// instance exiting must not delete the PID file of a background daemon
// started for the same directory — that would make the background daemon
// invisible to CheckExistingDaemon and allow a double start.
func CleanupDaemon(workingDir string) {
	pidPath, err := PIDFilePath(workingDir)
	if err != nil {
		return
	}
	info, err := ReadPIDFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
			// Corrupt file is garbage; safe to remove.
			_ = os.Remove(pidPath)
			return
		}
		// Unreadable for other reasons (#520 semantics): do not delete —
		// it may belong to a live daemon.
		debug.Log("daemon", "CleanupDaemon: PID file %s unreadable, NOT removing: %v", pidPath, err)
		return
	}
	if info.PID != os.Getpid() {
		debug.Log("daemon", "CleanupDaemon: PID file %s owned by pid %d (not us %d); keeping it", pidPath, info.PID, os.Getpid())
		return
	}
	_ = os.Remove(pidPath)
}

// FormatPID returns a human-readable PID string.
func FormatPID(pid int) string {
	return strconv.Itoa(pid)
}
