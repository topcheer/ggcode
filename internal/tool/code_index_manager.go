package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	started    bool             // true after StartBackgroundIndex has been called
	lastSearch time.Time        // last time Search() was called; used for idle release
	dirtyFiles map[string]int64 // path → known mtime at last index
	indexPath  string           // disk cache path
	workingDir string
	stopCh     chan struct{}
	rebuildCh  chan struct{}              // signaled by MarkDirty to trigger immediate debounced rebuild
	lockFile   *os.File                   // cross-process flock handle
	onReady    func(stats CodeIndexStats) // optional callback when index build completes

	// indexStats tracks basic stats for debugging/logging.
	stats CodeIndexStats
}

type CodeIndexStats struct {
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
	codeIndexRebuildTick  = 5 * time.Minute // periodic dirty-check interval (reduced frequency for lower CPU)

	// codeIndexRebuildDebounce is the delay after the last MarkDirty call
	// before triggering an incremental rebuild. This batches rapid edits
	// (e.g. multi_file_edit) into a single rebuild instead of rebuilding
	// after each file.
	codeIndexRebuildDebounce = 3 * time.Second
)

// codeIndexIdleRelease is how long the index sits idle (no Search calls)
// before being released from memory. The disk cache persists, so the next
// Search will reload from disk. Set to 10 minutes.
const codeIndexIdleRelease = 10 * time.Minute

// NewCodeIndexManager creates a manager for the given working directory.
// The index path is derived from a hash of the absolute path so that
// different workspaces get separate caches.
func NewCodeIndexManager(workingDir string) *CodeIndexManager {
	m := &CodeIndexManager{
		workingDir: workingDir,
		dirtyFiles: make(map[string]int64),
		stopCh:     make(chan struct{}),
		rebuildCh:  make(chan struct{}, 1), // buffered: non-blocking signal
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
// This is safe to call multiple times — if already building or started, it's a no-op.
// The method returns immediately; all work happens in a goroutine.
//
// A cross-process file lock ensures only one ggcode instance builds the
// index for a given workspace at a time. If another instance holds the
// lock, this instance skips the build and reads whatever cache exists.
func (m *CodeIndexManager) StartBackgroundIndex() {
	m.mu.Lock()
	if m.building || m.started {
		m.mu.Unlock()
		return
	}
	m.building = true
	m.started = true
	m.lastSearch = time.Now()
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

		// Start periodic dirty-file checker + idle release monitor.
		safego.Go("codeindex.dirtycheck", m.backgroundLoop)
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
	m.stats = CodeIndexStats{
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
		terms := tokenizeForIndex(string(data)) // index without expansion
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
	m.stats = CodeIndexStats{
		TotalFiles:   len(files),
		IndexedFiles: len(docs),
		UpdatedAt:    time.Now(),
	}
	onReady := m.onReady
	stats := m.stats
	m.mu.Unlock()

	debug.Log("codeindex", "index ready: %d docs, %d cached, %d skipped, build in %s",
		len(docs), indexed-len(docs), skipped, time.Since(start))

	if onReady != nil {
		onReady(stats)
	}
}

// SetOnReady registers a callback fired when the index build completes
// (both fresh builds and disk-cache loads). Used by REPL to show a system
// message when @ fuzzy search becomes available.
func (m *CodeIndexManager) SetOnReady(fn func(stats CodeIndexStats)) {
	m.mu.Lock()
	m.onReady = fn
	// If already ready, fire immediately.
	if m.ready {
		stats := m.stats
		m.mu.Unlock()
		if fn != nil {
			fn(stats)
		}
		return
	}
	m.mu.Unlock()
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
				name == ".venv" || name == ".tox" || name == ".ggcode" {
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

// backgroundLoop combines periodic dirty-file checking, on-demand
// debounced rebuilds triggered by MarkDirty, and idle memory release.
//
// Every codeIndexRebuildTick (5 min) the periodic check runs.
// Additionally, when MarkDirty signals rebuildCh, a debounced rebuild is
// triggered within codeIndexRebuildDebounce (3s), so the index reflects
// edits promptly instead of waiting up to 5 minutes.
//
// Idle release: if the index hasn't been searched in codeIndexIdleRelease
// (10 min), the in-memory index is freed. Disk cache persists.
func (m *CodeIndexManager) backgroundLoop() {
	ticker := time.NewTicker(codeIndexRebuildTick)
	defer ticker.Stop()

	// debounceTimer fires after a quiescent period following MarkDirty.
	var debounceTimer *time.Timer

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.periodicCheck()
		case <-m.rebuildCh:
			// Reset debounce: batch rapid edits into a single rebuild.
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.NewTimer(codeIndexRebuildDebounce)
		case <-func() <-chan time.Time {
			if debounceTimer == nil {
				return nil
			}
			return debounceTimer.C
		}():
			debounceTimer = nil
			m.rebuildDirty("debounced")
		}
	}
}

// periodicCheck handles the 5-minute periodic tick: idle release and
// dirty-file rebuild.
func (m *CodeIndexManager) periodicCheck() {
	// Check for idle release first.
	m.mu.RLock()
	idle := time.Since(m.lastSearch)
	ready := m.ready
	m.mu.RUnlock()

	if ready && idle > codeIndexIdleRelease {
		m.mu.Lock()
		if m.index != nil {
			debug.Log("codeindex", "idle for %v, releasing in-memory index (%d docs)", idle.Round(time.Minute), len(m.index.docs))
			m.index = nil
			m.ready = false
			m.stats = CodeIndexStats{}
		}
		m.mu.Unlock()
		return
	}

	m.rebuildDirty("periodic")
}

// rebuildDirty performs a true incremental update for dirty files.
// Only the changed files are re-read and re-tokenized; the existing BM25
// index (DF map, avgLength) is updated in-place rather than rebuilt from
// scratch. Deleted files are removed from the index.
// reason is used for debug logging ("debounced", "periodic").
func (m *CodeIndexManager) rebuildDirty(reason string) {
	m.mu.RLock()
	dirtyCount := len(m.dirtyFiles)
	ready := m.ready
	m.mu.RUnlock()
	if dirtyCount == 0 || !ready {
		return
	}
	// Only update if we can acquire the cross-process lock.
	if !m.tryLock() {
		debug.Log("codeindex", "%s rebuild: skipping, lock held by another instance", reason)
		m.mu.Lock()
		m.dirtyFiles = make(map[string]int64)
		m.mu.Unlock()
		return
	}

	// Snapshot dirty files under lock, then clear.
	m.mu.Lock()
	dirty := make(map[string]int64, len(m.dirtyFiles))
	for k, v := range m.dirtyFiles {
		dirty[k] = v
	}
	m.dirtyFiles = make(map[string]int64)
	idx := m.index // hold reference; we'll mutate a copy
	m.mu.Unlock()

	if idx == nil {
		debug.Log("codeindex", "%s rebuild: index nil, falling back to full build", reason)
		ctx, cancel := context.WithTimeout(context.Background(), codeIndexBuildTimeout)
		m.doBuild(ctx)
		cancel()
		m.unlock()
		return
	}

	debug.Log("codeindex", "%s incremental: %d files changed", reason, len(dirty))
	start := time.Now()

	// Build a path→index lookup for the existing docs.
	docIdx := make(map[string]int, len(idx.docs))
	for i, d := range idx.docs {
		docIdx[d.path] = i
	}

	updated, added, removed := 0, 0, 0
	for dirtyPath := range dirty {
		// Normalize to relative path. MarkDirty may receive absolute or relative paths.
		relPath := dirtyPath
		if filepath.IsAbs(dirtyPath) {
			if rp, err := filepath.Rel(m.workingDir, dirtyPath); err == nil {
				relPath = rp
			}
		}
		absPath := filepath.Join(m.workingDir, relPath)
		info, err := os.Stat(absPath)
		if err != nil {
			// File deleted or inaccessible: remove from index.
			if di, ok := docIdx[relPath]; ok {
				m.removeDocFromIndex(idx, di)
				delete(docIdx, relPath)
				removed++
			}
			continue
		}
		if info.IsDir() || info.Size() > codeIndexMaxFileSize {
			continue
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}
		terms := tokenizeForIndex(string(data))
		newTF := make(map[string]int, len(terms))
		for _, t := range terms {
			newTF[t]++
		}
		newDoc := bm25Doc{path: relPath, tf: newTF, length: len(terms)}

		if i, ok := docIdx[relPath]; ok {
			// Existing doc: replace in-place.
			m.replaceDocInIndex(idx, i, newDoc)
			updated++
		} else {
			// New file: append.
			idx.docs = append(idx.docs, newDoc)
			docIdx[relPath] = len(idx.docs) - 1
			for term := range newTF {
				idx.df[term]++
			}
			added++
		}
	}

	// Recalculate avgLength.
	totalLen := 0
	for _, d := range idx.docs {
		totalLen += d.length
	}
	if len(idx.docs) > 0 {
		idx.avgLength = float64(totalLen) / float64(len(idx.docs))
	}

	// Persist updated index to disk.
	m.persistIndex(context.Background(), idx.docs)

	// Update stats.
	m.mu.Lock()
	m.index = idx
	m.stats = CodeIndexStats{
		TotalFiles:   len(idx.docs),
		IndexedFiles: len(idx.docs),
		UpdatedAt:    time.Now(),
	}
	m.mu.Unlock()

	m.unlock()

	debug.Log("codeindex", "%s incremental done: %d updated, %d added, %d removed in %s",
		reason, updated, added, removed, time.Since(start))
}

// removeDocFromIndex removes the doc at index i from the BM25 index,
// updating DF counts and shrinking the docs slice. Uses swap-with-last
// for O(1) removal.
func (m *CodeIndexManager) removeDocFromIndex(idx *bm25Index, i int) {
	doc := idx.docs[i]
	// Decrement DF for this doc's terms.
	for term := range doc.tf {
		idx.df[term]--
		if idx.df[term] <= 0 {
			delete(idx.df, term)
		}
	}
	// Swap with last element, then truncate.
	last := len(idx.docs) - 1
	idx.docs[i] = idx.docs[last]
	idx.docs = idx.docs[:last]
}

// replaceDocInIndex replaces the doc at index i with newDoc, updating
// DF counts for terms that were added or removed.
func (m *CodeIndexManager) replaceDocInIndex(idx *bm25Index, i int, newDoc bm25Doc) {
	oldDoc := idx.docs[i]
	// Decrement DF for old terms.
	for term := range oldDoc.tf {
		idx.df[term]--
		if idx.df[term] <= 0 {
			delete(idx.df, term)
		}
	}
	// Increment DF for new terms.
	for term := range newDoc.tf {
		idx.df[term]++
	}
	idx.docs[i] = newDoc
}

// MarkDirty records that the given files have been modified and signals
// the background loop to trigger a debounced incremental rebuild. This is
// non-blocking and safe to call from the agent loop.
func (m *CodeIndexManager) MarkDirty(paths []string) {
	m.mu.Lock()
	for _, p := range paths {
		m.dirtyFiles[p] = time.Now().Unix()
	}
	ready := m.ready
	started := m.started
	m.mu.Unlock()

	// Only signal a rebuild if the index is ready and the background loop
	// is running. During initial build or after idle release, the periodic
	// tick will pick up dirty files.
	if ready && started {
		select {
		case m.rebuildCh <- struct{}{}:
		default: // already pending — the debounce timer will handle it
		}
	}
}

// Search queries the BM25 index. Returns an error if the index is not
// yet ready (still building in the background). If the index was released
// due to idle timeout, Search triggers a background reload from disk.
// FilePathFuzzy returns up to maxResults file paths from the index whose
// basename or path fuzzy-matches the query (subsequence match). If the index
// is not ready, returns nil. This reuses the already-indexed file list,
// avoiding a fresh directory walk.
func (m *CodeIndexManager) FilePathFuzzy(query string, maxResults int) []string {
	m.mu.RLock()
	ready := m.ready
	idx := m.index
	m.mu.RUnlock()

	if !ready || idx == nil {
		return nil
	}

	query = strings.ToLower(query)
	var results []string
	for _, doc := range idx.docs {
		// Fuzzy match on the full relative path, not just basename.
		// This lets "tui/comp" match "internal/tui/completion.go".
		if fuzzySubsequenceMatch(strings.ToLower(doc.path), query) {
			results = append(results, doc.path)
			if len(results) >= maxResults {
				break
			}
		}
	}
	return results
}

// fuzzySubsequenceMatch returns true if all characters of query appear in s
// in the same order (not necessarily contiguous). Same algorithm as VS Code
// file search (e.g. "cml" matches "completion").
func fuzzySubsequenceMatch(s, query string) bool {
	if query == "" {
		return true
	}
	si, qi := 0, 0
	for si < len(s) && qi < len(query) {
		if s[si] == query[qi] {
			qi++
		}
		si++
	}
	return qi == len(query)
}

func (m *CodeIndexManager) Search(query string, maxResults int) ([]bm25Result, error) {
	m.mu.Lock()
	m.lastSearch = time.Now()
	ready := m.ready
	building := m.building
	m.mu.Unlock()

	if !ready {
		// If not currently building, trigger a lazy reload from disk.
		if !building {
			m.lazyLoad()
		}
		return nil, errIndexNotReady
	}

	m.mu.RLock()
	idx := m.index
	m.mu.RUnlock()
	if idx == nil {
		return nil, errIndexNotReady
	}

	terms := tokenizeForSearch(query)
	if len(terms) == 0 {
		return nil, nil
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	return idx.score(terms, maxResults), nil
}

// lazyLoad starts a background disk-cache reload if the index was released
// due to idle timeout. This is non-blocking; Search will return
// errIndexNotReady until the reload completes.

// IsReady returns true if the index is available for queries.
func (m *CodeIndexManager) IsReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

// Stats returns basic index statistics for debugging.
func (m *CodeIndexManager) Stats() CodeIndexStats {
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

// lazyLoad starts a background disk-cache reload if the index was released
// due to idle timeout. This is non-blocking; Search will return
// errIndexNotReady until the reload completes.
func (m *CodeIndexManager) lazyLoad() {
	m.mu.Lock()
	if m.building || m.ready {
		m.mu.Unlock()
		return
	}
	m.building = true
	m.mu.Unlock()

	safego.Go("codeindex.lazyload", func() {
		defer func() {
			m.mu.Lock()
			m.building = false
			m.mu.Unlock()
		}()

		if !m.tryLock() {
			debug.Log("codeindex", "lazy load: lock held, reading cache without lock")
			m.loadDiskCache()
			return
		}
		defer m.unlock()

		ctx, cancel := context.WithTimeout(context.Background(), codeIndexBuildTimeout)
		defer cancel()
		m.doBuild(ctx)
		debug.Log("codeindex", "lazy load complete from disk cache")
	})
}

// errIndexNotReady is returned when Search is called before the
// background build completes.
var errIndexNotReady = &indexNotReadyError{}

type indexNotReadyError struct{}

func (e *indexNotReadyError) Error() string {
	return "code index is being built in the background, please try again in a few seconds"
}
