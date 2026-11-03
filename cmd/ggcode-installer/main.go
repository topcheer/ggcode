package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/install"
)

func main() {
	var version string
	var dir string

	flag.StringVar(&version, "version", "latest", "Release version to install (default: latest)")
	flag.StringVar(&dir, "dir", "", "Install directory (default: GOBIN, GOPATH/bin, or ~/go/bin)")
	flag.Parse()

	result, err := install.Install(context.Background(), install.Options{
		Version:        version,
		Dir:            dir,
		BaseURL:        strings.TrimSpace(os.Getenv("GGCODE_INSTALL_BASE_URL")),
		PlatformGOOS:   strings.TrimSpace(os.Getenv("GGCODE_INSTALL_GOOS")),
		PlatformGOARCH: strings.TrimSpace(os.Getenv("GGCODE_INSTALL_GOARCH")),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ggcode installer failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Installed %s to %s\n", filepath.Base(result.Path), result.Path)
	if !isOnPath(filepath.Dir(result.Path)) {

		if changed, err := install.EnsureOnPath(filepath.Dir(result.Path)); err != nil {
			fmt.Printf("Note: %s is not on your PATH yet and updating it failed: %v\n", filepath.Dir(result.Path), err)
		} else if changed {
			fmt.Printf("Note: added %s to your PATH - open a NEW terminal (or restart your session) before running ggcode.\n", filepath.Dir(result.Path))
		} else {
			fmt.Printf("Note: %s is not on your PATH for the CURRENT session; it will be available in new terminals.\n", filepath.Dir(result.Path))
		}
	}
}

func isOnPath(dir string) bool {
	dir = filepath.Clean(dir)
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(strings.TrimSpace(entry)) == dir {
			return true
		}
	}

	if found, err := exec.LookPath("ggcode"); err == nil {
		if abs, err := filepath.Abs(found); err == nil {
			return filepath.Clean(abs) == dir
		}
	}
	return false
}
