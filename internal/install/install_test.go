package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectTarget(t *testing.T) {
	target, err := DetectTarget("darwin", "amd64")
	if err != nil {
		t.Fatalf("DetectTarget returned error: %v", err)
	}
	if target.ArchiveName != "ggcode_darwin_x86_64.tar.gz" {
		t.Fatalf("unexpected archive name: %s", target.ArchiveName)
	}
	if target.BinaryName != "ggcode" {
		t.Fatalf("unexpected binary name: %s", target.BinaryName)
	}
}

func TestDetectTargetWindows(t *testing.T) {
	target, err := DetectTarget("windows", "arm64")
	if err != nil {
		t.Fatalf("DetectTarget returned error: %v", err)
	}
	if target.ArchiveName != "ggcode_windows_arm64.zip" {
		t.Fatalf("unexpected archive name: %s", target.ArchiveName)
	}
	if target.BinaryName != "ggcode.exe" {
		t.Fatalf("unexpected binary name: %s", target.BinaryName)
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"":       "latest",
		"latest": "latest",
		"1.2.3":  "v1.2.3",
		"v1.2.3": "v1.2.3",
	}
	for input, want := range cases {
		if got := NormalizeVersion(input); got != want {
			t.Fatalf("NormalizeVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestReleaseURLs(t *testing.T) {
	target, _ := DetectTarget("linux", "arm64")
	if got := AssetURL("1.2.3", target); got != "https://github.com/topcheer/ggcode/releases/download/v1.2.3/ggcode_linux_arm64.tar.gz" {
		t.Fatalf("unexpected asset URL: %s", got)
	}
	if got := ChecksumsURL("latest"); got != "https://github.com/topcheer/ggcode/releases/latest/download/checksums.txt" {
		t.Fatalf("unexpected checksums URL: %s", got)
	}
}

func TestReleaseURLsWithBaseURL(t *testing.T) {
	target, _ := DetectTarget("linux", "amd64")
	if got := assetURLForBase("https://example.test/ggcode", "1.2.3", target); got != "https://example.test/ggcode/releases/download/v1.2.3/ggcode_linux_x86_64.tar.gz" {
		t.Fatalf("unexpected asset URL: %s", got)
	}
	if got := checksumsURLForBase("https://example.test/ggcode", "latest"); got != "https://example.test/ggcode/releases/latest/download/checksums.txt" {
		t.Fatalf("unexpected checksums URL: %s", got)
	}
}

func TestReleaseSourcesDefaultsToGitHubOnly(t *testing.T) {
	t.Setenv(updateBaseURLsEnv, "")
	got := releaseSources("")
	if len(got) != 1 || got[0].baseURL != "https://github.com/topcheer/ggcode" || got[0].proxyPrefix != "" {
		t.Fatalf("unexpected default release sources: %#v", got)
	}
}

func TestReleaseSourcesUsesEnvOverride(t *testing.T) {
	t.Setenv(updateBaseURLsEnv, " https://mirror-one.example/topcheer/ggcode , https://get.ystone.us/ \nhttps://mirror-one.example/topcheer/ggcode ")
	got := releaseSources("")
	if len(got) != 3 {
		t.Fatalf("unexpected env release sources: %#v", got)
	}
	if got[0].baseURL != "https://mirror-one.example/topcheer/ggcode" {
		t.Fatalf("unexpected first source: %#v", got[0])
	}
	if got[1].baseURL != "https://get.ystone.us" {
		t.Fatalf("unexpected second source: %#v", got[1])
	}
	if got[2].proxyPrefix != "https://get.ystone.us/" {
		t.Fatalf("unexpected third source: %#v", got[2])
	}
}

func TestParseChecksums(t *testing.T) {
	checksums := parseChecksums("abc123  ggcode_linux_x86_64.tar.gz\nxyz789 ggcode_windows_x86_64.zip\n")
	if got := checksums["ggcode_windows_x86_64.zip"]; got != "xyz789" {
		t.Fatalf("unexpected checksum: %q", got)
	}
}

func TestResolveInstallDirUsesGOBIN(t *testing.T) {
	t.Setenv("GOBIN", "/tmp/custom-bin")
	t.Setenv("GOPATH", "")

	got, err := ResolveInstallDir("")
	if err != nil {
		t.Fatalf("ResolveInstallDir returned error: %v", err)
	}
	if got != "/tmp/custom-bin" {
		t.Fatalf("unexpected install dir: %s", got)
	}
}

func TestResolveInstallDirUsesFirstGOPATH(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", strings.Join([]string{"/tmp/go1", "/tmp/go2"}, string(os.PathListSeparator)))

	got, err := ResolveInstallDir("")
	if err != nil {
		t.Fatalf("ResolveInstallDir returned error: %v", err)
	}
	if got != filepath.Join("/tmp/go1", "bin") {
		t.Fatalf("unexpected install dir: %s", got)
	}
}

func TestDownloadBinaryWithBaseURLTarGz(t *testing.T) {
	t.Helper()

	archiveName := "ggcode_linux_x86_64.tar.gz"
	binaryData := []byte("linux-binary")
	archiveData := makeTarGzArchive(t, "ggcode", binaryData)
	checksum := sha256.Sum256(archiveData)
	checksumBody := fmt.Sprintf("%s  %s\n", hex.EncodeToString(checksum[:]), archiveName)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/download/v1.2.3/" + archiveName:
			_, _ = w.Write(archiveData)
		case "/releases/download/v1.2.3/checksums.txt":
			_, _ = w.Write([]byte(checksumBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := DownloadBinary(context.Background(), Options{
		Version:        "v1.2.3",
		BaseURL:        server.URL,
		PlatformGOOS:   "linux",
		PlatformGOARCH: "amd64",
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatalf("DownloadBinary returned error: %v", err)
	}
	if result.Version != "v1.2.3" {
		t.Fatalf("unexpected version: %s", result.Version)
	}
	if string(result.BinaryData) != string(binaryData) {
		t.Fatalf("unexpected binary data: %q", string(result.BinaryData))
	}
}

func TestDownloadBinaryWithBaseURLZip(t *testing.T) {
	t.Helper()

	archiveName := "ggcode_windows_x86_64.zip"
	binaryData := []byte("windows-binary")
	archiveData := makeZipArchive(t, "ggcode.exe", binaryData)
	checksum := sha256.Sum256(archiveData)
	checksumBody := fmt.Sprintf("%s  %s\n", hex.EncodeToString(checksum[:]), archiveName)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/download/v1.2.3/" + archiveName:
			_, _ = w.Write(archiveData)
		case "/releases/download/v1.2.3/checksums.txt":
			_, _ = w.Write([]byte(checksumBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := DownloadBinary(context.Background(), Options{
		Version:        "v1.2.3",
		BaseURL:        server.URL,
		PlatformGOOS:   "windows",
		PlatformGOARCH: "amd64",
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatalf("DownloadBinary returned error: %v", err)
	}
	if string(result.BinaryData) != string(binaryData) {
		t.Fatalf("unexpected binary data: %q", string(result.BinaryData))
	}
}

func TestResolveReleaseVersionFallsBackToMirror(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer primary.Close()

	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/v1.2.4", http.StatusFound)
		case "/releases/tag/v1.2.4":
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mirror.Close()

	restore := overrideReleaseBaseURLs(t, primary.URL, mirror.URL)
	defer restore()

	version, err := ResolveReleaseVersion(context.Background(), http.DefaultClient, "latest")
	if err != nil {
		t.Fatalf("ResolveReleaseVersion returned error: %v", err)
	}
	if version != "v1.2.4" {
		t.Fatalf("unexpected version: %s", version)
	}
}

func TestDownloadBinaryFallsBackToMirror(t *testing.T) {
	archiveName := "ggcode_linux_x86_64.tar.gz"
	binaryData := []byte("linux-binary")
	archiveData := makeTarGzArchive(t, "ggcode", binaryData)
	checksum := sha256.Sum256(archiveData)
	checksumBody := fmt.Sprintf("%s  %s\n", hex.EncodeToString(checksum[:]), archiveName)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer primary.Close()

	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/v1.2.3", http.StatusFound)
		case "/releases/tag/v1.2.3":
			_, _ = w.Write([]byte("ok"))
		case "/releases/download/v1.2.3/" + archiveName:
			_, _ = w.Write(archiveData)
		case "/releases/download/v1.2.3/checksums.txt":
			_, _ = w.Write([]byte(checksumBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mirror.Close()

	restore := overrideReleaseBaseURLs(t, primary.URL, mirror.URL)
	defer restore()

	result, err := DownloadBinary(context.Background(), Options{
		Version:        "latest",
		PlatformGOOS:   "linux",
		PlatformGOARCH: "amd64",
		HTTPClient:     http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("DownloadBinary returned error: %v", err)
	}
	if result.Version != "v1.2.3" {
		t.Fatalf("unexpected version: %s", result.Version)
	}
	if string(result.BinaryData) != string(binaryData) {
		t.Fatalf("unexpected binary data: %q", string(result.BinaryData))
	}
	if !strings.HasPrefix(result.ArchiveURL, mirror.URL+"/") {
		t.Fatalf("expected mirror archive URL, got %s", result.ArchiveURL)
	}
}

func TestResolveReleaseVersionWithProxyPrefix(t *testing.T) {
	apiBody := []byte(`{"tag_name":"v1.2.5"}`)
	apiZip := makeZipArchive(t, "latest", apiBody)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/https://api.github.com/repos/topcheer/ggcode/releases/latest" {
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(apiZip)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	t.Setenv(updateBaseURLsEnv, server.URL+"/")
	version, err := resolveReleaseVersion(context.Background(), server.Client(), "", "latest")
	if err != nil {
		t.Fatalf("resolveReleaseVersion returned error: %v", err)
	}
	if version != "v1.2.5" {
		t.Fatalf("unexpected version: %s", version)
	}
}

func TestDownloadBinaryWithProxyPrefix(t *testing.T) {
	archiveName := "ggcode_linux_x86_64.tar.gz"
	binaryData := []byte("linux-binary")
	archiveData := makeTarGzArchive(t, "ggcode", binaryData)
	archiveZip := makeZipArchive(t, archiveName, archiveData)
	checksum := sha256.Sum256(archiveData)
	checksumBody := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(checksum[:]), archiveName))
	checksumZip := makeZipArchive(t, "checksums.txt", checksumBody)
	apiZip := makeZipArchive(t, "latest", []byte(`{"tag_name":"v1.2.3"}`))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/https://api.github.com/repos/topcheer/ggcode/releases/latest":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(apiZip)
		case "/https://github.com/topcheer/ggcode/releases/download/v1.2.3/" + archiveName:
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archiveZip)
		case "/https://github.com/topcheer/ggcode/releases/download/v1.2.3/checksums.txt":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(checksumZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv(updateBaseURLsEnv, server.URL+"/")
	result, err := DownloadBinary(context.Background(), Options{
		Version:        "latest",
		PlatformGOOS:   "linux",
		PlatformGOARCH: "amd64",
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatalf("DownloadBinary returned error: %v", err)
	}
	if result.Version != "v1.2.3" {
		t.Fatalf("unexpected version: %s", result.Version)
	}
	if string(result.BinaryData) != string(binaryData) {
		t.Fatalf("unexpected binary data: %q", string(result.BinaryData))
	}
}

func overrideReleaseBaseURLs(t *testing.T, urls ...string) func() {
	t.Helper()
	prev := append([]string(nil), defaultReleaseBaseURLs...)
	defaultReleaseBaseURLs = append([]string(nil), urls...)
	return func() {
		defaultReleaseBaseURLs = prev
	}
}

func makeTarGzArchive(t *testing.T, name string, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func makeZipArchive(t *testing.T, name string, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}
