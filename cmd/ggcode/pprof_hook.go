package main

import (
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/safego"
)

// pprofEndpoint resolves the pprof listen port. GGCODE_PPROF wins; otherwise
// the marker file ~/.ggcode/pprof-port (contents: a port number) enables it.
// The marker file exists because macOS cannot inject environment variables
// into an already-running or relaunched process - ops creates the file, the
// NEXT relaunch exposes the profile endpoints, no launch-env change needed.
func pprofEndpoint() (string, bool) {
	if p := os.Getenv("GGCODE_PPROF"); p != "" {
		return p, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(home, ".ggcode", "pprof-port"))
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(string(b))
	if n, err := strconv.Atoi(p); err == nil && n > 0 && n < 65536 {
		return p, true
	}
	return "", false
}

func init() {
	if p, ok := pprofEndpoint(); ok {
		safego.Go("pprof.listen", func() {
			// Loopback only: profiles are operator-local diagnostics.
			_ = http.ListenAndServe("127.0.0.1:"+p, nil)
		})
	}
}
