package tool

import (
	"bytes"
	"go/format"
	"path/filepath"
)

// formatGoBytes applies gofmt formatting to Go source files (.go).
//
// Non-Go files and code that fails to parse (e.g. an edit-in-progress or a
// generated fragment) are returned unchanged with changed=false. Returns the
// (possibly reformatted) bytes and whether formatting changed the content.
//
// NOTE: Automatic unused-import removal was permanently removed because the
// AST-based heuristic could not reliably determine the actual package name
// for versioned module paths like gopkg.in/yaml.v3 (identifier "yaml" but
// path segment "yaml.v3"). This caused build-breaking import deletions across
// multiple releases. Use goimports or editor LSP for import management.
func formatGoBytes(path string, data []byte) ([]byte, bool) {
	if filepath.Ext(path) != ".go" {
		return data, false
	}

	formatted, err := format.Source(data)
	if err != nil {
		// Can't parse — return unchanged (safe fallback).
		return data, false
	}

	if bytes.Equal(formatted, data) {
		return data, false
	}

	return formatted, true
}
