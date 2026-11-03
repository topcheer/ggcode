package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var spaFS embed.FS

var spafs fs.FS

func init() {
	var err error
	spafs, err = fs.Sub(spaFS, "dist")
	if err != nil {
		panic(err)
	}
}
