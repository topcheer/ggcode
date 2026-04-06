package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type Service struct {
	CurrentVersion string
	ExecPath       string
	ConfigPath     string
	WorkDir        string
	WrapperKind    string
	CheckTTL       time.Duration
	HTTPClient     *http.Client
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
	cmd := exec.Command(prepared.HelperPath, "update-helper", "--manifest", prepared.ManifestPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	cmd.Dir = firstNonEmpty(s.WorkDir, mustGetwd())
	cmd.Env = os.Environ()
	return cmd.Start()
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
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for _, target := range manifest.TargetPaths {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create target dir for %s: %w", target, err)
		}
		for {
			lastErr = install.WriteExecutable(target, sourceData)
			if lastErr == nil {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("replace %s: %w", target, lastErr)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	_ = os.Remove(manifest.SourceBinary)
	_ = os.Remove(manifestPath)

	cmd := exec.Command(manifest.RestartPath, manifest.RestartArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = firstNonEmpty(manifest.WorkingDir, mustGetwd())
	cmd.Env = os.Environ()
	return cmd.Start()
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
	return http.DefaultClient
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
