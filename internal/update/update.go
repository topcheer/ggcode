package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/install"
	"github.com/topcheer/ggcode/internal/version"
)

const (
	wrapperKindNative = "native"
	wrapperKindNPM    = "npm"
	wrapperKindPython = "python"

	defaultCheckTTL = 12 * time.Hour
)

var ErrAlreadyUpToDate = errors.New("already up to date")

// ErrNeedsElevation indicates the target directory requires elevated
// privileges. On Windows the caller should relaunch the helper with UAC.
var ErrNeedsElevation = errors.New("update requires elevation")

type Service struct {
	CurrentVersion string
	ExecPath       string
	ConfigPath     string
	WorkDir        string
	WrapperKind    string
	CheckTTL       time.Duration
	HTTPClient     *http.Client
	needsElevation bool // set by checkWritable; helper should launch elevated
}

type CheckResult struct {
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version"`
	HasUpdate      bool      `json:"has_update"`
	CheckedAt      time.Time `json:"checked_at"`
}

type PreparedUpdate struct {
	Version      string
	HelperPath   string
	ManifestPath string
}

type HelperManifest struct {
	ParentPID       int      `json:"parent_pid"`
	SourceBinary    string   `json:"source_binary"`
	TargetPaths     []string `json:"target_paths"`
	RestartPath     string   `json:"restart_path"`
	RestartArgs     []string `json:"restart_args"`
	WorkingDir      string   `json:"working_dir"`
	ExpectedVersion string   `json:"expected_version"`
}

// ReadStagedBinary reads a manifest file and returns the path to the staged
// (downloaded) binary. Used by the restart helper to know which file to install.
func ReadStagedBinary(manifestPath string) (string, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("read manifest: %w", err)
	}
	var manifest HelperManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("parse manifest: %w", err)
	}
	return manifest.SourceBinary, nil
}

type cachedCheck struct {
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version"`
	CheckedAt      time.Time `json:"checked_at"`
}

func NewService(currentVersion, execPath, configPath, workDir string) *Service {
	return &Service{
		CurrentVersion: strings.TrimSpace(currentVersion),
		ExecPath:       strings.TrimSpace(execPath),
		ConfigPath:     strings.TrimSpace(configPath),
		WorkDir:        strings.TrimSpace(workDir),
		WrapperKind:    detectWrapperKind(execPath),
		CheckTTL:       defaultCheckTTL,
	}
}

func (s *Service) Check(ctx context.Context) (CheckResult, error) {
	result := CheckResult{
		CurrentVersion: versionStringOrDev(s.CurrentVersion),
		CheckedAt:      time.Now(),
	}
	if !isComparableRelease(result.CurrentVersion) {
		return result, nil
	}
	if cached, ok := s.readCachedCheck(); ok {
		return CheckResult{
			CurrentVersion: result.CurrentVersion,
			LatestVersion:  cached.LatestVersion,
			HasUpdate:      isNewerRelease(cached.LatestVersion, result.CurrentVersion),
			CheckedAt:      cached.CheckedAt,
		}, nil
	}

	latest, err := install.ResolveReleaseVersion(ctx, s.httpClient(), "latest")
	if err != nil {
		return result, err
	}
	result.LatestVersion = latest
	result.HasUpdate = isNewerRelease(latest, result.CurrentVersion)
	_ = s.writeCachedCheck(cachedCheck{
		CurrentVersion: result.CurrentVersion,
		LatestVersion:  latest,
		CheckedAt:      result.CheckedAt,
	})
	return result, nil
}

func (s *Service) Prepare(ctx context.Context, resumeID string) (PreparedUpdate, error) {
	check, err := s.Check(ctx)
	if err != nil {
		return PreparedUpdate{}, err
	}
	if !check.HasUpdate {
		return PreparedUpdate{}, ErrAlreadyUpToDate
	}

	// Pre-flight: verify write permissions BEFORE downloading.
	// On Windows, the target binary dir and the helper staging dir must
	// both be writable, otherwise the update will fail after a large download.
	needsElevation, err := s.checkWritable()
	if err != nil {
		return PreparedUpdate{}, err
	}
	s.needsElevation = needsElevation

	downloaded, err := install.DownloadBinary(ctx, install.Options{
		Version:    check.LatestVersion,
		HTTPClient: s.httpClient(),
	})
	if err != nil {
		return PreparedUpdate{}, err
	}
	helperDir := filepath.Join(config.ConfigDir(), "update-helper")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		return PreparedUpdate{}, fmt.Errorf("create update helper dir: %w", err)
	}

	helperPath := filepath.Join(helperDir, helperBinaryName())
	if err := copyFile(s.ExecPath, helperPath, 0o755); err != nil {
		return PreparedUpdate{}, fmt.Errorf("prepare update helper: %w", err)
	}

	sourceBinary := filepath.Join(helperDir, downloaded.Target.BinaryName+".download")
	if err := install.WriteExecutable(sourceBinary, downloaded.BinaryData); err != nil {
		return PreparedUpdate{}, fmt.Errorf("stage downloaded binary: %w", err)
	}

	targetPaths, restartPath, err := s.resolveTargetPaths(check.LatestVersion)
	if err != nil {
		return PreparedUpdate{}, err
	}
	manifest := HelperManifest{
		ParentPID:       os.Getpid(),
		SourceBinary:    sourceBinary,
		TargetPaths:     targetPaths,
		RestartPath:     restartPath,
		RestartArgs:     s.restartArgs(resumeID),
		WorkingDir:      firstNonEmpty(s.WorkDir, mustGetwd()),
		ExpectedVersion: check.LatestVersion,
	}
	manifestPath := filepath.Join(helperDir, fmt.Sprintf("manifest-%d.json", time.Now().UnixNano()))
	data, err := json.Marshal(manifest)
	if err != nil {
		return PreparedUpdate{}, fmt.Errorf("marshal update manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return PreparedUpdate{}, fmt.Errorf("write update manifest: %w", err)
	}
	return PreparedUpdate{
		Version:      check.LatestVersion,
		HelperPath:   helperPath,
		ManifestPath: manifestPath,
	}, nil
}

func (s *Service) LaunchHelper(prepared PreparedUpdate) error {
	cmd := s.helperCommand(prepared)

	// On Windows, if the target dir needs elevation, relaunch the helper
	// with UAC (ShellExecute "runas" verb).
	if s.needsElevation && runtime.GOOS == "windows" {
		return launchElevated(cmd)
	}

	return cmd.Start()
}

// ApplyBinary replaces the binary files directly without spawning a helper
// process or starting a new instance. This is used on Unix where the running
// binary can be overwritten in place. After ApplyBinary returns, the caller
// should use restart.ExecSelf (syscall.Exec) to load the new binary, keeping
// the same PID, process group, and terminal control.
func (s *Service) ApplyBinary(prepared PreparedUpdate) error {
	data, err := os.ReadFile(prepared.ManifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var manifest HelperManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	sourceData, err := os.ReadFile(manifest.SourceBinary)
	if err != nil {
		return fmt.Errorf("read staged binary: %w", err)
	}
	for _, target := range manifest.TargetPaths {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create target dir for %s: %w", target, err)
		}
		// On Unix, the running binary can be overwritten directly — no retry needed.
		if err := install.WriteExecutable(target, sourceData); err != nil {
			return fmt.Errorf("replace %s: %w", target, err)
		}
	}
	_ = os.Remove(manifest.SourceBinary)
	_ = os.Remove(prepared.ManifestPath)
	return nil
}

func RunHelper(manifestPath string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var manifest HelperManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	sourceData, err := os.ReadFile(manifest.SourceBinary)
	if err != nil {
		return fmt.Errorf("read staged binary: %w", err)
	}
	// #1402-A: targets are replaced one by one; a failure halfway (target
	// locked by antivirus/indexer past the 30s deadline) used to leave the
	// EARLIER targets on the new version with no rollback - a half-updated
	// install (npm/python wrappers carry 2 target paths) with no repair
	// entry point. Back up each target's previous bytes (nil = did not
	// exist) and restore them best-effort on failure. A restore that also
	// fails is reported but does not mask the original error; the manifest
	// is kept so the helper can simply be re-run.
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	type backup struct {
		path    string
		data    []byte
		existed bool
	}
	var written []backup
	// #1423-A/U-D2: EVERY abort path must roll back the targets this
	// run already replaced - the old code only rolled back from the
	// deadline branch; an unreadable target (this fix) or a MkdirAll
	// failure (U-D2) returned early and left a half-updated install,
	// exactly the state #1402 promised to eliminate.
	rollbackAll := func(curErr error) error {
		var restoreErrs []string
		for i := len(written) - 1; i >= 0; i-- {
			w := written[i]
			if !w.existed {
				if rmErr := os.Remove(w.path); rmErr != nil {
					restoreErrs = append(restoreErrs, fmt.Sprintf("remove fresh %s: %v", w.path, rmErr))
				}
				continue
			}
			if rsErr := install.WriteExecutable(w.path, w.data); rsErr != nil {
				restoreErrs = append(restoreErrs, fmt.Sprintf("restore %s: %v", w.path, rsErr))
			}
		}
		if len(restoreErrs) > 0 {
			return fmt.Errorf("%w (rollback incomplete: %s)", curErr, strings.Join(restoreErrs, "; "))
		}
		return fmt.Errorf("%w (earlier targets rolled back)", curErr)
	}
	for _, target := range manifest.TargetPaths {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			// U-D2: used to return WITHOUT rolling back earlier targets.
			return rollbackAll(fmt.Errorf("create target dir for %s: %w", target, err))
		}
		var prev []byte
		existed := false
		if old, err := os.ReadFile(target); err == nil {
			prev, existed = old, true
		} else if !errors.Is(err, fs.ErrNotExist) {
			// #1423-A: ANY read error (AV scan lock, ACL deny, mode 0o200
			// exists-but-unreadable) used to be conflated with 'does not
			// exist'. The replace itself is tmp+rename and only needs
			// DIRECTORY write - it succeeds where read failed - and the
			// rollback then os.Remove'd the never-backed-up original: the
			// user's binary deleted forever. Unreadable-but-present ABORTS
			// the run (with rollback of earlier targets); os.Remove
			// rollback is only correct for targets that truly did not
			// exist.
			return rollbackAll(fmt.Errorf("target %s exists but is unreadable (no backup possible, refusing to replace): %w", target, err))
		}
		for {
			lastErr = install.WriteExecutable(target, sourceData)
			if lastErr == nil {
				written = append(written, backup{path: target, data: prev, existed: existed})
				break
			}
			if time.Now().After(deadline) {
				return rollbackAll(fmt.Errorf("replace %s: %w", target, lastErr))
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	_ = os.Remove(manifest.SourceBinary)
	_ = os.Remove(manifestPath)

	cmd := restartCommand(manifest)
	return cmd.Start()
}

func (s *Service) helperCommand(prepared PreparedUpdate) *exec.Cmd {
	cmd := exec.Command(prepared.HelperPath, "update-helper", "--manifest", prepared.ManifestPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Keep the original terminal attached so the restarted TUI can reacquire it
	// after the helper swaps binaries and spawns the fresh process.
	cmd.Stdin = os.Stdin
	cmd.Dir = firstNonEmpty(s.WorkDir, mustGetwd())
	cmd.Env = os.Environ()
	return cmd
}

func restartCommand(manifest HelperManifest) *exec.Cmd {
	cmd := exec.Command(manifest.RestartPath, manifest.RestartArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = firstNonEmpty(manifest.WorkingDir, mustGetwd())
	cmd.Env = os.Environ()
	return cmd
}

func (s *Service) restartArgs(resumeID string) []string {
	args := make([]string, 0, 4)
	if strings.TrimSpace(s.ConfigPath) != "" {
		args = append(args, "--config", s.ConfigPath)
	}
	if strings.TrimSpace(resumeID) != "" {
		args = append(args, "--resume", resumeID)
	}
	return args
}

func (s *Service) resolveTargetPaths(latestVersion string) ([]string, string, error) {
	execPath := strings.TrimSpace(s.ExecPath)
	if execPath == "" {
		return nil, "", fmt.Errorf("resolve executable path")
	}
	paths := []string{execPath}
	restartPath := execPath
	if latestPath, ok := wrapperLatestPath(execPath, latestVersion); ok {
		paths = append(paths, latestPath)
		restartPath = latestPath
	}
	paths = uniquePaths(paths)
	return paths, restartPath, nil
}

func wrapperLatestPath(execPath, latestVersion string) (string, bool) {
	binaryDir := filepath.Dir(execPath)
	versionDir := filepath.Dir(binaryDir)
	rootDir := filepath.Dir(versionDir)
	kind := filepath.Base(rootDir)
	if kind != wrapperKindNPM && kind != wrapperKindPython {
		return "", false
	}
	currentVersion := filepath.Base(versionDir)
	if currentVersion == "" || latestVersion == "" || currentVersion == latestVersion {
		return "", false
	}
	return filepath.Join(rootDir, latestVersion, filepath.Base(binaryDir), filepath.Base(execPath)), true
}

func detectWrapperKind(execPath string) string {
	if kind := strings.TrimSpace(os.Getenv("GGCODE_WRAPPER_KIND")); kind != "" {
		return kind
	}
	pattern := regexp.MustCompile(`(?i)[/\\]ggcode[/\\](npm|python)[/\\][^/\\]+[/\\][^/\\]+[/\\]ggcode(?:\.exe)?$`)
	match := pattern.FindStringSubmatch(filepath.ToSlash(execPath))
	if len(match) == 2 {
		return strings.ToLower(match[1])
	}
	return wrapperKindNative
}

func (s *Service) readCachedCheck() (cachedCheck, bool) {
	path := s.cachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return cachedCheck{}, false
	}
	var cached cachedCheck
	if err := json.Unmarshal(data, &cached); err != nil {
		return cachedCheck{}, false
	}
	if cached.CurrentVersion != versionStringOrDev(s.CurrentVersion) {
		return cachedCheck{}, false
	}
	ttl := s.CheckTTL
	if ttl <= 0 {
		ttl = defaultCheckTTL
	}
	if time.Since(cached.CheckedAt) > ttl {
		return cachedCheck{}, false
	}
	return cached, true
}

func (s *Service) writeCachedCheck(cached cachedCheck) error {
	path := s.cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *Service) cachePath() string {
	return filepath.Join(config.ConfigDir(), "update-check.json")
}

func (s *Service) httpClient() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	// Fall back to the install package default (bounded Timeout) instead of
	// http.DefaultClient (Timeout == 0): a half-open or black-holed mirror
	// must not hang /update forever (#976).
	return install.DefaultHTTPClient()
}

func helperBinaryName() string {
	name := "ggcode-update-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func isComparableRelease(v string) bool {
	_, ok := parseReleaseVersion(v)
	return ok
}

func isNewerRelease(candidate, current string) bool {
	a, okA := parseReleaseVersion(candidate)
	b, okB := parseReleaseVersion(current)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if a[i] > b[i] {
			return true
		}
		if a[i] < b[i] {
			return false
		}
	}
	return false
}

func parseReleaseVersion(v string) ([3]int, bool) {
	var parsed [3]int
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return parsed, false
	}
	for i, part := range parts {
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				return parsed, false
			}
			n = n*10 + int(r-'0')
		}
		parsed[i] = n
	}
	return parsed, true
}

func uniquePaths(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(out, value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func versionStringOrDev(v string) string {
	if strings.TrimSpace(v) == "" {
		return version.Display()
	}
	return strings.TrimSpace(v)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

// checkWritable verifies that all paths needed for a self-update are writable.
// Returns (needsElevation=true) if the staging dir is writable but the target
// dir is not — the caller can then relaunch the helper with elevation.
// Returns a hard error only if even the staging dir is inaccessible.
func (s *Service) checkWritable() (bool, error) {
	// 1. Helper staging dir (~/.ggcode/update-helper/) — must be writable.
	helperDir := filepath.Join(config.ConfigDir(), "update-helper")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		return false, fmt.Errorf("update: cannot create staging directory %s: %w\n"+
			"Check that you have write permission to this location.", helperDir, err)
	}
	probe := filepath.Join(helperDir, ".write-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return false, fmt.Errorf("update: staging directory %s is not writable: %w\n"+
			"Check that you have write permission to this location.", helperDir, err)
	}
	_ = os.Remove(probe)

	// 2. Target executable directory — may need elevation.
	execDir := filepath.Dir(s.ExecPath)
	if execDir == "." || execDir == "" {
		execDir = mustGetwd()
	}
	probe2 := filepath.Join(execDir, ".ggcode-update-probe")
	if err := os.WriteFile(probe2, []byte("x"), 0o644); err != nil {
		if runtime.GOOS == "windows" {
			// On Windows we can request UAC elevation for the helper.
			return true, nil
		}
		return false, fmt.Errorf("update: target directory %s is not writable: %w\n"+
			"Try running with sudo or reinstall to a user-writable location.", execDir, err)
	}
	_ = os.Remove(probe2)

	return false, nil
}
