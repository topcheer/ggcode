package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	owner      = "topcheer"
	repo       = "ggcode"
	binaryName = "ggcode"
)

var defaultReleaseBaseURLs = []string{
	fmt.Sprintf("https://github.com/%s/%s", owner, repo),
	fmt.Sprintf("https://relv.xgheaven.com.cn/%s/%s", owner, repo),
}

type Target struct {
	GOOS        string
	GOARCH      string
	ArchiveName string
	BinaryName  string
}

type Options struct {
	Version string
	Dir     string

	PlatformGOOS   string
	PlatformGOARCH string
	HTTPClient     *http.Client
	BaseURL        string
}

type Result struct {
	Path       string
	Version    string
	ArchiveURL string
}

type BinaryResult struct {
	Version    string
	Target     Target
	BinaryData []byte
	ArchiveURL string
}

func Install(ctx context.Context, opts Options) (Result, error) {
	binary, err := DownloadBinary(ctx, opts)
	if err != nil {
		return Result{}, err
	}
	installDir, err := ResolveInstallDir(opts.Dir)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create install dir: %w", err)
	}

	binaryPath := filepath.Join(installDir, binary.Target.BinaryName)
	if err := WriteExecutable(binaryPath, binary.BinaryData); err != nil {
		return Result{}, err
	}

	return Result{
		Path:       binaryPath,
		Version:    binary.Version,
		ArchiveURL: binary.ArchiveURL,
	}, nil
}

func DownloadBinary(ctx context.Context, opts Options) (BinaryResult, error) {
	goos := firstNonEmpty(opts.PlatformGOOS, runtime.GOOS)
	goarch := firstNonEmpty(opts.PlatformGOARCH, runtime.GOARCH)
	target, err := DetectTarget(goos, goarch)
	if err != nil {
		return BinaryResult{}, err
	}

	version, err := resolveReleaseVersion(ctx, opts.HTTPClient, opts.BaseURL, opts.Version)
	if err != nil {
		return BinaryResult{}, err
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	var errs []error
	for _, baseURL := range releaseBaseURLs(opts.BaseURL) {
		archiveURL := assetURLForBase(baseURL, version, target)
		checksumURL := checksumsURLForBase(baseURL, version)

		archiveData, err := download(ctx, client, archiveURL)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s archive: %w", archiveURL, err))
			continue
		}
		checksumData, err := download(ctx, client, checksumURL)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s checksums: %w", checksumURL, err))
			continue
		}
		if err := verifyArchive(target.ArchiveName, archiveData, checksumData); err != nil {
			errs = append(errs, fmt.Errorf("%s verify: %w", baseURL, err))
			continue
		}

		binaryData, err := extractBinary(target, archiveData)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s extract: %w", archiveURL, err))
			continue
		}

		return BinaryResult{
			Target:     target,
			Version:    version,
			BinaryData: binaryData,
			ArchiveURL: archiveURL,
		}, nil
	}
	return BinaryResult{}, fmt.Errorf("download binary: %w", errors.Join(errs...))
}

func DetectTarget(goos, goarch string) (Target, error) {
	archiveExt := ".tar.gz"
	if goos == "windows" {
		archiveExt = ".zip"
	}

	assetArch := goarch
	switch goarch {
	case "amd64":
		assetArch = "x86_64"
	case "arm64":
		assetArch = "arm64"
	default:
		return Target{}, fmt.Errorf("unsupported architecture: %s", goarch)
	}

	switch goos {
	case "linux", "darwin", "windows":
	default:
		return Target{}, fmt.Errorf("unsupported operating system: %s", goos)
	}

	name := fmt.Sprintf("%s_%s_%s%s", binaryName, goos, assetArch, archiveExt)
	exeName := binaryName
	if goos == "windows" {
		exeName += ".exe"
	}

	return Target{
		GOOS:        goos,
		GOARCH:      goarch,
		ArchiveName: name,
		BinaryName:  exeName,
	}, nil
}

func NormalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || strings.EqualFold(version, "latest") {
		return "latest"
	}
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

func AssetURL(version string, target Target) string {
	return assetURLForBase("", version, target)
}

func ChecksumsURL(version string) string {
	return checksumsURLForBase("", version)
}

func ReleaseBaseURL(version string) string {
	return releaseBaseURLForBase("", version)
}

func ResolveReleaseVersion(ctx context.Context, client *http.Client, version string) (string, error) {
	return resolveReleaseVersion(ctx, client, "", version)
}

func releaseBaseURLForBase(baseURL, version string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf("https://github.com/%s/%s", owner, repo)
	}
	version = NormalizeVersion(version)
	if version != "latest" {
		return baseURL + "/releases/download/" + version
	}
	return baseURL + "/releases/latest/download"
}

func assetURLForBase(baseURL, version string, target Target) string {
	return releaseBaseURLForBase(baseURL, version) + "/" + target.ArchiveName
}

func checksumsURLForBase(baseURL, version string) string {
	return releaseBaseURLForBase(baseURL, version) + "/checksums.txt"
}

func resolveReleaseVersion(ctx context.Context, client *http.Client, baseURL, version string) (string, error) {
	version = NormalizeVersion(version)
	if version != "latest" {
		return version, nil
	}
	if client == nil {
		client = http.DefaultClient
	}
	var errs []error
	for _, candidate := range releaseBaseURLs(baseURL) {
		version, err := resolveReleaseVersionForBase(ctx, client, candidate)
		if err == nil {
			return version, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", candidate, err))
	}
	return "", fmt.Errorf("resolve latest release: %w", errors.Join(errs...))
}

func resolveReleaseVersionForBase(ctx context.Context, client *http.Client, baseURL string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultReleaseBaseURLs[0]
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("resolve latest release returned %s", resp.Status)
	}
	finalURL := resp.Request.URL.Path
	const marker = "/releases/tag/"
	idx := strings.Index(finalURL, marker)
	if idx < 0 {
		return "", fmt.Errorf("could not resolve latest release from %s", resp.Request.URL.String())
	}
	return strings.TrimSpace(finalURL[idx+len(marker):]), nil
}

func releaseBaseURLs(baseURL string) []string {
	if baseURL = strings.TrimSpace(baseURL); baseURL != "" {
		return []string{strings.TrimRight(baseURL, "/")}
	}
	out := make([]string, 0, len(defaultReleaseBaseURLs))
	seen := make(map[string]struct{}, len(defaultReleaseBaseURLs))
	for _, candidate := range defaultReleaseBaseURLs {
		candidate = strings.TrimRight(strings.TrimSpace(candidate), "/")
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func ResolveInstallDir(explicit string) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit, nil
	}
	if gobin := strings.TrimSpace(os.Getenv("GOBIN")); gobin != "" {
		return gobin, nil
	}
	if gopath := strings.TrimSpace(os.Getenv("GOPATH")); gopath != "" {
		first := strings.Split(gopath, string(os.PathListSeparator))[0]
		if first != "" {
			return filepath.Join(first, "bin"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "go", "bin"), nil
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func verifyArchive(assetName string, archiveData, checksumData []byte) error {
	expected, ok := parseChecksums(string(checksumData))[assetName]
	if !ok {
		return fmt.Errorf("checksum for %s not found", assetName)
	}
	sum := sha256.Sum256(archiveData)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("checksum mismatch for %s", assetName)
	}
	return nil
}

func parseChecksums(body string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		out[fields[len(fields)-1]] = fields[0]
	}
	return out
}

func extractBinary(target Target, archiveData []byte) ([]byte, error) {
	if strings.HasSuffix(target.ArchiveName, ".zip") {
		return extractZipBinary(target.BinaryName, archiveData)
	}
	return extractTarGzBinary(target.BinaryName, archiveData)
}

func extractZipBinary(name string, archiveData []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archiveData), int64(len(archiveData)))
	if err != nil {
		return nil, fmt.Errorf("read zip: %w", err)
	}
	for _, file := range reader.File {
		if filepath.Base(file.Name) != name {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open binary in zip: %w", err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("binary %s not found in archive", name)
}

func extractTarGzBinary(name string, archiveData []byte) ([]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return nil, fmt.Errorf("read gzip: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != name {
			continue
		}
		return io.ReadAll(tr)
	}
	return nil, fmt.Errorf("binary %s not found in archive", name)
}

func WriteExecutable(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return fmt.Errorf("write binary: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil && runtime.GOOS != "windows" {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmp)
		return fmt.Errorf("remove old binary: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("move binary into place: %w", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
