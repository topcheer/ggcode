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
