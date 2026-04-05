package install

import (
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
