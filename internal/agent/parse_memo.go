package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sync"
)

const parseMemoMaxEntries = 8

type parseMemoEntry struct {
	filePath string
	mode     parser.Mode
	content  string
	file     *ast.File
	fset     *token.FileSet
	err      error
}

var parseMemo struct {
	sync.Mutex
	entries []parseMemoEntry
}

func resetParseMemo() {
	parseMemo.Lock()
	parseMemo.entries = nil
	parseMemo.Unlock()
}

func parseGoSource(filePath, src string, mode parser.Mode) (*ast.File, *token.FileSet, error) {
	key := func(e parseMemoEntry) bool {
		return e.filePath == filePath && e.mode == mode && e.content == src
	}

	parseMemo.Lock()
	for i, e := range parseMemo.entries {
		if key(e) {

			copy(parseMemo.entries[1:i+1], parseMemo.entries[:i])
			parseMemo.entries[0] = e
			parseMemo.Unlock()
			return e.file, e.fset, e.err
		}
	}

	parseMemo.Unlock()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, src, mode)

	entry := parseMemoEntry{filePath: filePath, mode: mode, content: src, file: file, fset: fset, err: err}

	parseMemo.Lock()
	parseMemo.entries = append(parseMemo.entries, parseMemoEntry{})
	copy(parseMemo.entries[1:], parseMemo.entries)
	parseMemo.entries[0] = entry
	if len(parseMemo.entries) > parseMemoMaxEntries {
		parseMemo.entries = parseMemo.entries[:parseMemoMaxEntries]
	}
	parseMemo.Unlock()
	return file, fset, err
}
