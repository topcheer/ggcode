package tool

import (
	"bytes"
	"go/format"
	"path/filepath"
)

func formatGoBytes(path string, data []byte) ([]byte, bool) {
	if filepath.Ext(path) != ".go" {
		return data, false
	}

	formatted, err := format.Source(data)
	if err != nil {

		return data, false
	}

	if bytes.Equal(formatted, data) {
		return data, false
	}

	return formatted, true
}
