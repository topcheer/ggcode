package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var spaFS embed.FS

// spafs is the embedded filesystem stripped of the "dist/" prefix.
var spafs fs.FS

func init() {
	var err error
	spafs, err = fs.Sub(spaFS, "dist")
	if err != nil {
		panic(err)
	}
}
