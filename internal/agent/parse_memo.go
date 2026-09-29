package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sync"
)

// parseGoSource memoizes the most recent parses keyed by
// (filePath, mode, source). The post-write integrity pipeline fans out to
// ~45 Go checks per write, most of which independently re-parse the same
// old/new content (under one of two parser modes); the memo collapses that
// redundant work while keeping each check's error-handling semantics
// identical to a direct parser.ParseFile call.
//
// A small bounded set suffices: a typical check run touches only a handful
// of distinct sources (old content, new content, occasionally a few
// sibling files); 8 slots comfortably covers that working set.
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
	entries []parseMemoEntry // newest-first ring, bounded by parseMemoMaxEntries
}

// resetParseMemo drops all cached parses. Called before each check run so
// entries never outlive the write that produced them by more than one run.
func resetParseMemo() {
	parseMemo.Lock()
	parseMemo.entries = nil
	parseMemo.Unlock()
}

// parseGoSource parses src as a Go file, returning exactly what
// parser.ParseFile would ((possibly partial) AST, FileSet, error) — except
// that repeated calls with identical (filePath, mode, src) within a check
// run return the cached result instead of re-parsing.
//
// filePath is part of the cache key: positions in the FileSet name it, so
// identical content parsed under different filenames must not share an
// entry (the rendered positions would name the wrong file).
//
// The returned (*ast.File, error) pair must be treated as read-only shared
// state: concurrent checks within one run observe the same AST. All
// registered checks are pure AST readers (no node mutation), and the
// pre-existing CheckContext.GoAST field established that sharing
// precedent.
func parseGoSource(filePath, src string, mode parser.Mode) (*ast.File, *token.FileSet, error) {
	key := func(e parseMemoEntry) bool {
		return e.filePath == filePath && e.mode == mode && e.content == src
	}

	parseMemo.Lock()
	for i, e := range parseMemo.entries {
		if key(e) {
			// Move-to-front: keeps the hot entries resident under churn.
			copy(parseMemo.entries[1:i+1], parseMemo.entries[:i])
			parseMemo.entries[0] = e
			parseMemo.Unlock()
			return e.file, e.fset, e.err
		}
	}
	// Parse outside the lock: parsing is the expensive part and independent
	// misses may parse concurrently without deadlocking.
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
