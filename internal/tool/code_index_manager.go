package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// CodeIndexManager maintains a persistent BM25 index of the workspace's
// source files. The index is built asynchronously in the background —
// the code_search tool reads from the index without blocking the agent loop.
//
// If the index is not yet ready (still building), Search returns an error
// directing the LLM to retry shortly. The index persists to disk so that
// subsequent sessions load the cache and only do incremental mtime-based
// updates.
type CodeIndexManager struct {
	mu         sync.RWMutex
	index      *bm25Index
	ready      bool
	building   bool
	dirtyFiles map[string]int64 // path → known mtime at last index
	indexPath  string           // disk cache path
	workingDir string
	stopCh     chan struct{}
	lockFile   *os.File // cross-process flock handle

	// indexStats tracks basic stats for debugging/logging.
	stats codeIndexStats
}

type codeIndexStats struct {
	TotalFiles   int       `json:"total_files"`
	IndexedFiles int       `json:"indexed_files"`
	IndexSize    int       `json:"index_size"` // approximate bytes
	UpdatedAt    time.Time `json:"updated_at"`
}

// persistedDoc is the on-disk representation of a single indexed file.
type persistedDoc struct {
	Path   string         `json:"path"`
	Mtime  int64          `json:"mtime"` // unix seconds
	TF     map[string]int `json:"tf"`    // term frequency
	Length int            `json:"length"`
}

// persistedIndex is the on-disk cache format.
type persistedIndex struct {
	Version    int            `json:"version"`
	WorkingDir string         `json:"working_dir"`
	UpdatedAt  time.Time      `json:"updated_at"`
	Docs       []persistedDoc `json:"docs"`
}

const codeIndexVersion = 1

// Max files / size limits to protect against pathological repos.
const (
	codeIndexMaxFiles     = 50000
	codeIndexMaxFileSize  = 256 * 1024 // 256 KB per file
	codeIndexBuildTimeout = 5 * time.Minute
	codeIndexRebuildTick  = 60 * time.Second // periodic dirty-check interval
)

// NewCodeIndexManager creates a manager for the given working directory.
// The index path is derived from a hash of the absolute path so that
// different workspaces get separate caches.
func NewCodeIndexManager(workingDir string) *CodeIndexManager {
	m := &CodeIndexManager{
		workingDir: workingDir,
		dirtyFiles: make(map[string]int64),
		stopCh:     make(chan struct{}),
	}
	m.indexPath = m.computeIndexPath()
	return m
}

// computeIndexPath returns ~/.ggcode/cache/codeindex/<hash>.json.
func (m *CodeIndexManager) computeIndexPath() string {
	abs, err := filepath.Abs(m.workingDir)
	if err != nil {
		abs = m.workingDir
	}
	h := sha256.Sum256([]byte(abs))
	hash := hex.EncodeToString(h[:8]) // 8 bytes = 16 hex chars, collision-safe for paths
	return filepath.Join(config.ConfigDir(), "cache", "codeindex", hash+".json")
}

// StartBackgroundIndex begins asynchronous index construction.
// This is safe to call multiple times — if already building, it's a no-op.
// The method returns immediately; all work happens in a goroutine.
//
// A cross-process file lock ensures only one ggcode instance builds the
// index for a given workspace at a time. If another instance holds the
// lock, this instance skips the build and reads whatever cache exists.
func (m *CodeIndexManager) StartBackgroundIndex() {
	m.mu.Lock()
	if m.building {
		m.mu.Unlock()
		return
	}
	m.building = true
	m.mu.Unlock()

	safego.Go("codeindex.background", func() {
		defer func() {
			m.mu.Lock()
			m.building = false
			m.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), codeIndexBuildTimeout)
		defer cancel()

		// Try to acquire cross-process lock. If another instance is
		// already building, we skip — the other instance will write
		// the index, and we'll pick it up on the next dirty-check cycle.
		if !m.tryLock() {
			debug.Log("codeindex", "another instance is building the index, skipping")
			// Still try to load the existing disk cache.
			m.loadDiskCache()
			return
		}
		defer m.unlock()

		m.doBuild(ctx)

		// Start periodic dirty-file checker.
		safego.Go("codeindex.dirtycheck", m.dirtyCheckLoop)
	})
}

// tryLock acquires a cross-process exclusive lock on the index lock file.
// Returns true if the lock was acquired, false if another process holds it.
// Uses non-blocking flock (LOCK_EX | LOCK_NB) on Unix.
func (m *CodeIndexManager) tryLock() bool {
	lockPath := m.indexPath + ".lock"
	// Ensure the directory exists.
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return false
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		debug.Log("codeindex", "lock: open error: %v", err)
		return false
	}
	// Non-blocking exclusive lock.
	if !lockFileExcl(f) {
		_ = f.Close()
		return false
	}
	m.lockFile = f
	return true
}

// unlock releases the cross-process lock.
func (m *CodeIndexManager) unlock() {
	if m.lockFile != nil {
		unlockFileExcl(m.lockFile)
		_ = m.lockFile.Close()
		m.lockFile = nil
	}
}

// loadDiskCache loads only the existing disk cache into memory without
// doing a full rebuild. Used when another instance holds the lock.
func (m *CodeIndexManager) loadDiskCache() {
	data, err := os.ReadFile(m.indexPath)
	if err != nil {
		return
	}
	var pi persistedIndex
	if json.Unmarshal(data, &pi) != nil || pi.Version != codeIndexVersion {
		return
	}
	if len(pi.Docs) == 0 {
		return
	}
	idx := &bm25Index{
		docs: make([]bm25Doc, 0, len(pi.Docs)),
		df:   make(map[string]int, 512),
	}
	var totalLength int
	for _, d := range pi.Docs {
		if len(d.TF) == 0 {
			continue
		}
		doc := bm25Doc{path: d.Path, tf: d.TF, length: d.Length}
		idx.docs = append(idx.docs, doc)
		totalLength += d.Length
		for term := range d.TF {
			idx.df[term]++
		}
	}
	if len(idx.docs) == 0 {
		return
	}
	idx.avgLength = float64(totalLength) / float64(len(idx.docs))

	m.mu.Lock()
	m.index = idx
	m.ready = true
	m.stats = codeIndexStats{
		TotalFiles:   len(idx.docs),
		IndexedFiles: len(idx.docs),
		UpdatedAt:    pi.UpdatedAt,
	}
	m.mu.Unlock()
	debug.Log("codeindex", "loaded %d docs from disk cache (locked by another instance)", len(idx.docs))
}

// doBuild loads the disk cache, does an mtime-diff incremental update,
// and marks the index as ready.
func (m *CodeIndexManager) doBuild(ctx context.Context) {
	start := time.Now()

	// Phase 1: Load existing disk cache.
	var cached []persistedDoc
	if data, err := os.ReadFile(m.indexPath); err == nil {
		var pi persistedIndex
		if json.Unmarshal(data, &pi) == nil && pi.Version == codeIndexVersion {
			cached = pi.Docs
			debug.Log("codeindex", "loaded %d docs from disk cache", len(cached))
		}
	}

	// Build a lookup of cached docs by path.
	cachedMap := make(map[string]persistedDoc, len(cached))
	for _, d := range cached {
		cachedMap[d.Path] = d
	}

	// Phase 2: Walk the working directory for current files.
	files := m.collectFiles(ctx)
	if len(files) == 0 {
		debug.Log("codeindex", "no indexable files found in %s", m.workingDir)
		return
	}

	// Phase 3: Incremental update — only re-tokenize changed/new files.
	var docs []bm25Doc
	var totalLength int
	skipped, indexed := 0, 0

	for _, absPath := range files {
		select {
		case <-ctx.Done():
			debug.Log("codeindex", "build timed out after indexing %d files", indexed)
			return
		default:
		}

		info, err := os.Stat(absPath)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() > codeIndexMaxFileSize {
			skipped++
			continue
		}

		relPath, _ := filepath.Rel(m.workingDir, absPath)
		mtime := info.ModTime().Unix()

		// Check cache: if mtime matches, reuse cached TF without re-reading.
		if cached, ok := cachedMap[relPath]; ok && cached.Mtime == mtime {
			if len(cached.TF) > 0 {
				docs = append(docs, bm25Doc{
					path:   relPath,
					tf:     cached.TF,
					length: cached.Length,
				})
				totalLength += cached.Length
				indexed++
				continue
			}
		}

		// Cache miss or stale: read and tokenize.
		data, err := os.ReadFile(absPath)
		if err != nil {
			skipped++
			continue
		}
		terms := tokenizeForSearch(string(data))
		if len(terms) == 0 {
			continue
		}
		tf := make(map[string]int, len(terms))
		for _, term := range terms {
			tf[term]++
		}
		docs = append(docs, bm25Doc{
			path:   relPath,
			tf:     tf,
			length: len(terms),
		})
		totalLength += len(terms)
		indexed++
	}

	if len(docs) == 0 {
		debug.Log("codeindex", "no documents indexed after walk")
		return
	}

	// Phase 4: Build the BM25 index (compute DF + avgdl).
	idx := &bm25Index{
		docs:      docs,
		df:        make(map[string]int, 512),
		avgLength: float64(totalLength) / float64(len(docs)),
	}
	for _, doc := range docs {
		for term := range doc.tf {
			idx.df[term]++
		}
	}

	// Phase 5: Persist to disk.
	m.persistIndex(ctx, docs)

	// Phase 6: Publish.
	m.mu.Lock()
	m.index = idx
	m.ready = true
	m.stats = codeIndexStats{
		TotalFiles:   len(files),
		IndexedFiles: len(docs),
		UpdatedAt:    time.Now(),
	}
	m.mu.Unlock()

	debug.Log("codeindex", "index ready: %d docs, %d cached, %d skipped, build in %s",
		len(docs), indexed-len(docs), skipped, time.Since(start))
}

// collectFiles walks the working directory and returns a list of
// indexable source files.
func (m *CodeIndexManager) collectFiles(ctx context.Context) []string {
	var files []string
	_ = filepath.WalkDir(m.workingDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" ||
				name == "dist" || name == "build" || name == ".next" ||
				name == "__pycache__" || name == ".cache" || name == "target" ||
				name == ".venv" || name == ".tox" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= codeIndexMaxFiles {
			return filepath.SkipAll
		}
		if isCodeFile(path) {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// isCodeFile returns true for common source file extensions.
func isCodeFile(path string) bool {
	ext := filepath.Ext(path)
	switch ext {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".py", ".rb", ".rs",
		".java", ".kt", ".swift", ".c", ".h", ".cpp", ".cc", ".hpp",
		".cs", ".php", ".scala", ".clj", ".ex", ".exs", ".erl",
		".vim", ".lua", ".pl", ".sh", ".bash", ".zsh", ".ps1",
		".sql", ".graphql", ".proto", ".thrift", ".dart", ".r",
		".yaml", ".yml", ".toml", ".json", ".xml", ".html", ".css", ".scss",
		".md", ".txt", ".rst":
		return true
	default:
		return false
	}
}

// persistIndex writes the index to disk atomically.
func (m *CodeIndexManager) persistIndex(ctx context.Context, docs []bm25Doc) {
	pi := persistedIndex{
		Version:    codeIndexVersion,
		WorkingDir: m.workingDir,
		UpdatedAt:  time.Now(),
		Docs:       make([]persistedDoc, 0, len(docs)),
	}
	for _, doc := range docs {
		absPath := filepath.Join(m.workingDir, doc.path)
		info, err := os.Stat(absPath)
		if err != nil {
			continue
		}
		pi.Docs = append(pi.Docs, persistedDoc{
			Path:   doc.path,
			Mtime:  info.ModTime().Unix(),
			TF:     doc.tf,
			Length: doc.length,
		})
	}

	data, err := json.Marshal(pi)
	if err != nil {
		debug.Log("codeindex", "persist: marshal error: %v", err)
		return
	}

	// Check disk size limit (~100 MB).
	if len(data) > 100*1024*1024 {
		debug.Log("codeindex", "persist: skipping write, index too large (%d bytes)", len(data))
		return
	}

	dir := filepath.Dir(m.indexPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		debug.Log("codeindex", "persist: mkdir error: %v", err)
		return
	}

	// Atomic write: temp file + rename.
	tmpPath := m.indexPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		debug.Log("codeindex", "persist: write error: %v", err)
		return
	}
	if err := os.Rename(tmpPath, m.indexPath); err != nil {
		debug.Log("codeindex", "persist: rename error: %v", err)
		_ = os.Remove(tmpPath)
		return
	}
	debug.Log("codeindex", "persisted %d docs to %s (%d bytes)", len(pi.Docs), m.indexPath, len(data))
}

// dirtyCheckLoop periodically checks for dirty files and triggers
// incremental re-indexing. Runs until Stop() is called.
func (m *CodeIndexManager) dirtyCheckLoop() {
	ticker := time.NewTicker(codeIndexRebuildTick)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.mu.RLock()
			dirtyCount := len(m.dirtyFiles)
			m.mu.RUnlock()
			if dirtyCount == 0 {
				continue
			}
			// Only rebuild if we can acquire the cross-process lock.
			// If another instance holds it, skip — they'll rebuild.
			if !m.tryLock() {
				debug.Log("codeindex", "dirty check: skipping rebuild, lock held by another instance")
				m.mu.Lock()
				m.dirtyFiles = make(map[string]int64)
				m.mu.Unlock()
				continue
			}
			debug.Log("codeindex", "dirty check: %d files changed, rebuilding", dirtyCount)
			ctx, cancel := context.WithTimeout(context.Background(), codeIndexBuildTimeout)
			m.doBuild(ctx)
			cancel()
			m.unlock()
			m.mu.Lock()
			m.dirtyFiles = make(map[string]int64)
			m.mu.Unlock()
		}
	}
}

// MarkDirty records that the given files have been modified.
// This is non-blocking and safe to call from the agent loop.
func (m *CodeIndexManager) MarkDirty(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range paths {
		m.dirtyFiles[p] = time.Now().Unix()
	}
}

// Search queries the BM25 index. Returns an error if the index is not
// yet ready (still building in the background).
func (m *CodeIndexManager) Search(query string, maxResults int) ([]bm25Result, error) {
	m.mu.RLock()
	if !m.ready || m.index == nil {
		m.mu.RUnlock()
		return nil, errIndexNotReady
	}
	idx := m.index
	m.mu.RUnlock()

	terms := tokenizeForSearch(query)
	if len(terms) == 0 {
		return nil, nil
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	return idx.score(terms, maxResults), nil
}

// IsReady returns true if the index is available for queries.
func (m *CodeIndexManager) IsReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

// Stats returns basic index statistics for debugging.
func (m *CodeIndexManager) Stats() codeIndexStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// Stop shuts down the background dirty-check goroutine and releases the lock.
func (m *CodeIndexManager) Stop() {
	select {
	case <-m.stopCh:
		// already closed
	default:
		close(m.stopCh)
	}
	m.unlock()
}

// errIndexNotReady is returned when Search is called before the
// background build completes.
var errIndexNotReady = &indexNotReadyError{}

type indexNotReadyError struct{}

func (e *indexNotReadyError) Error() string {
	return "code index is being built in the background, please try again in a few seconds"
}
