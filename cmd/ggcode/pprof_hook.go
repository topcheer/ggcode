package main

import (
	"net/http"
	_ "net/http/pprof"
	"os"

	"github.com/topcheer/ggcode/internal/safego"
)

func init() {
	if p := os.Getenv("GGCODE_PPROF"); p != "" {
		safego.Go("pprof.listen", func() {

			_ = http.ListenAndServe("127.0.0.1:"+p, nil)
		})
	}
}
