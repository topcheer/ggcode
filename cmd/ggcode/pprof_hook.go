package main

// Permanent runtime diagnostics hook (#3699): when GGCODE_PPROF is set to a
// port number (e.g. GGCODE_PPROF=6060 ggcode), net/http/pprof serves on
// 127.0.0.1:<port> for the process lifetime - so memory leaks and CPU
// hotspots in long-lived TUI sessions can be diagnosed live. It found the
// #3699 GB-scale retention (internal/memory toolflow slurping whole session
// files) in one capture. Loopback-only, env-gated: default surface unchanged.

import (
	"net/http"
	_ "net/http/pprof"
	"os"

	"github.com/topcheer/ggcode/internal/safego"
)

func init() {
	if p := os.Getenv("GGCODE_PPROF"); p != "" {
		safego.Go("pprof.listen", func() {
			// Best-effort diagnostics listener; a taken port just means the
			// hook stays off - never worth crashing startup over.
			_ = http.ListenAndServe("127.0.0.1:"+p, nil)
		})
	}
}
