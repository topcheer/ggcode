package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	mu           sync.RWMutex
	index        *bm25Index
	ready        bool
	building     bool
	started      bool             // true after StartBackgroundIndex has been called
	lastActivity time.Time        // last time Search() or MarkDirty() was called; used for idle release
	dirtyFiles   map[string]int64 // path → known mtime at last index
	indexPath    string           // disk cache path
	workingDir   string
	stopCh       chan struct{}
	rebuildCh    chan struct{}              // signaled by MarkDirty to trigger immediate debounced rebuild
	lockFile     *os.File                   // cross-process flock handle
	onReady      func(stats CodeIndexStats) // optional callback when index build completes

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

	// codeIndexMaxTotalTerms caps the SUM of per-document distinct terms
	// held in memory (each tf map entry costs ~50-90 bytes with a Go map
	// string key). Without this budget, a working directory that parents
	// many repositories (e.g. starting ggcode in ~/projects instead of a
	// single repo) easily yields 50k files x thousands of terms = tens of
	// gigabytes of resident tf/df maps. The budget is the primary memory
	// guard; the file-count cap alone is not. ~1.5M terms keeps the index
	// under roughly a few hundred MB while still covering large monorepos.
	// Override with GGCODE_CODE_INDEX_MAX_TERMS (0 restores the old
	// unbounded behavior and is not recommended).
	codeIndexMaxTotalTerms = 1_500_000

	// codeIndexRebuildDebounce is the delay after the last MarkDirty call
	// before triggering an incremental rebuild. This batches rapid edits
	// (e.g. multi_file_edit) into a single rebuild instead of rebuilding
	// after each file.
	codeIndexRebuildDebounce = 3 * time.Second
)

// codeIndexIdleRelease is how long the index sits with no activity (no
// Search or MarkDirty calls) before being released from memory. This ensures
// active editing sessions keep the index alive, while truly idle sessions
// eventually free memory. Disk cache persists. Set to 60 minutes.
const codeIndexIdleRelease = 60 * time.Minute

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
	m.lastActivity = time.Now()
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

	// Build a lookup of cached docs by path, filtering out paths that
	// fall under now-skipped directories (e.g. .ggcode, node_modules).
	cachedMap := make(map[string]persistedDoc, len(cached))
	filtered := 0
	for _, d := range cached {
		if isInSkipDir(d.Path) {
			filtered++
			continue
		}
		cachedMap[d.Path] = d
	}
	if filtered > 0 {
		debug.Log("codeindex", "filtered %d cached docs in skip dirs", filtered)
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
	var totalTerms int
	truncated := false
	skipped, indexed := 0, 0
	maxTerms := codeIndexMaxTotalTermsOverride()

	for _, absPath := range files {
		if maxTerms > 0 && totalTerms >= maxTerms {
			truncated = true
			break
		}
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
				if maxTerms > 0 && totalTerms+len(cached.TF) > maxTerms {
					truncated = true
					break
				}
				docs = append(docs, bm25Doc{
					path:   relPath,
					tf:     cached.TF,
					length: cached.Length,
				})
				totalTerms += len(cached.TF)
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
		if maxTerms > 0 && totalTerms+len(tf) > maxTerms {
			truncated = true
			break
		}
		docs = append(docs, bm25Doc{
			path:   relPath,
			tf:     tf,
			length: len(terms),
		})
		totalTerms += len(tf)
		totalLength += len(terms)
		indexed++
	}
	if truncated {
		debug.Log("codeindex", "term budget reached (%d terms over %d docs) - index truncated at %d of %d files; "+
			"set GGCODE_CODE_INDEX_MAX_TERMS to adjust, or start ggcode inside a single project",
			totalTerms, len(docs), len(docs), len(files))
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
			if isSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			// Nested git repositories are SEMANTICALLY foreign - a
			// parent-dir start (multi-project workspace) or a vendored
			// clone previously had every sibling source file of the
			// inner .git indexed into THIS project's BM25 corpus,
			// crowding real files out of the 50k quota and burning the
			// #1625 term budget on foreign trees (tens of GB on
			// disk-heavy parents). Skip the whole nested repo root.
			if path != m.workingDir {
				if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
					return filepath.SkipDir
				}
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

// isInSkipDir checks if any path component is a skip directory.
// Used to purge stale cache entries from directories that were later
// added to the skip list.
func isInSkipDir(relPath string) bool {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, p := range parts {
		if isSkipDir(p) {
			return true
		}
	}
	return false
}

// isSkipDir returns true for directories that should be excluded from
// indexing: version control, dependency/vendor dirs, build outputs,
// temp dirs, and tool-specific caches across all major languages.
func isSkipDir(name string) bool {
	switch name {
	// VCS & meta
	case ".git", ".svn", ".hg", ".bzr", ".ggcode", ".idea", ".vscode":
		return true
	// JS/TS
	case "node_modules", "bower_components", ".next", ".nuxt", ".svelte-kit",
		".turbo", ".parcel-cache", "coverage", ".cypress":
		return true
	// Python
	case "__pycache__", ".venv", "venv", ".tox", ".mypy_cache", ".pytest_cache",
		".ruff_cache", "site-packages", "eggs", ".eggs":
		return true
	// Java / JVM / Rust / Go (shared dirs)
	case "target", ".gradle", ".maven", "build", "out", "vendor":
		return true
	// Ruby / PHP / general
	case ".bundle", "tmp", ".husky", ".pnp", ".yarn":
		return true
	// C/C++
	case "cmake-build-debug", "cmake-build-release":
		return true
	// Swift / Xcode
	case "DerivedData", ".build":
		return true
	// General build output & temp
	case "dist", "bin", "obj", ".cache", "temp", ".tmp":
		return true
	}
	return false
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

	dir := filepath.Dir(m.indexPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		debug.Log("codeindex", "persist: mkdir error: %v", err)
		return
	}

	// Stream-encode to the temp file instead of json.Marshal: building the
	// whole JSON in one buffer used to double peak memory (marshal buffer on
	// top of the tf maps), which for large workspaces pushed the process
	// into swap. json.Encoder writes incrementally.
	tmpPath := m.indexPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		debug.Log("codeindex", "persist: create error: %v", err)
		return
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	marshalErr := enc.Encode(&pi)
	if cerr := f.Close(); cerr != nil && marshalErr == nil {
		marshalErr = cerr
	}
	if marshalErr != nil {
		debug.Log("codeindex", "persist: encode error: %v", marshalErr)
		_ = os.Remove(tmpPath)
		return
	}

	// Check disk size limit (~100 MB) AFTER writing, then decide whether to
	// keep the temp file (rename) or drop it.
	if info, err := os.Stat(tmpPath); err == nil && info.Size() > 100*1024*1024 {
		debug.Log("codeindex", "persist: discarding cache, index too large (%d bytes)", info.Size())
		_ = os.Remove(tmpPath)
		return
	}
	if err := os.Rename(tmpPath, m.indexPath); err != nil {
		debug.Log("codeindex", "persist: rename error: %v", err)
		_ = os.Remove(tmpPath)
		return
	}
	debug.Log("codeindex", "persisted %d docs to %s", len(pi.Docs), m.indexPath)
}

// codeIndexMaxTotalTermsOverride reads GGCODE_CODE_INDEX_MAX_TERMS once.
// Values < 0 disable the budget (legacy unbounded behavior); parse errors
// fall back to the compiled-in default.
func codeIndexMaxTotalTermsOverride() int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("GGCODE_CODE_INDEX_MAX_TERMS")))
	if err != nil {
		return codeIndexMaxTotalTerms
	}
	if v < 0 {
		return 0 // unbounded
	}
	return v
}

// backgroundLoop combines periodic dirty-file checking, on-demand
// debounced rebuilds triggered by MarkDirty, and idle memory release.
//
// Every codeIndexRebuildTick (5 min) the periodic check runs.
// Additionally, when MarkDirty signals rebuildCh, a debounced rebuild is
// triggered within codeIndexRebuildDebounce (3s), so the index reflects
// edits promptly instead of waiting up to 5 minutes.
//
// Idle release: if the index hasn't had any activity (no Search or MarkDirty
// calls) in codeIndexIdleRelease (60 min), the in-memory index is freed.
// Disk cache persists.
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
	idle := time.Since(m.lastActivity)
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

	// Detect externally-modified files (e.g. via run_command, file_ops,
	// or external editors) that weren't caught by MarkDirty.
	m.scanForExternalChanges()

	m.rebuildDirty("periodic")
}

// scanForExternalChanges walks the working directory and compares current
// file mtimes against the indexed mtimes. Any file that is new, modified,
// or deleted since the last index update is added to dirtyFiles so the next
// rebuildDirty picks it up. This catches changes from run_command, file_ops,
// or any other source that bypasses MarkDirty.
func (m *CodeIndexManager) scanForExternalChanges() {
	m.mu.RLock()
	idx := m.index
	m.mu.RUnlock()
	if idx == nil {
		return
	}

	// Build a snapshot of indexed paths → mtime from the persisted cache
	// to avoid re-reading the disk cache file. We use the in-memory index
	// plus a disk-cache mtime lookup.
	indexedPaths := make(map[string]bool, len(idx.docs))
	for _, doc := range idx.docs {
		indexedPaths[doc.path] = true
	}

	// Load mtimes from disk cache for comparison.
	var cachedMap map[string]int64
	if data, err := os.ReadFile(m.indexPath); err == nil {
		var pi persistedIndex
		if json.Unmarshal(data, &pi) == nil && pi.Version == codeIndexVersion {
			cachedMap = make(map[string]int64, len(pi.Docs))
			for _, d := range pi.Docs {
				cachedMap[d.Path] = d.Mtime
			}
		}
	}
	if cachedMap == nil {
		return // no cache to compare against
	}

	found := make(map[string]bool)
	newDirty := 0

	_ = filepath.WalkDir(m.workingDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if isSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isCodeFile(path) {
			return nil
		}
		relPath, _ := filepath.Rel(m.workingDir, path)
		found[relPath] = true

		info, err := os.Stat(path)
		if err != nil || info.Size() > codeIndexMaxFileSize {
			return nil
		}
		mtime := info.ModTime().Unix()
		cachedMtime, wasIndexed := cachedMap[relPath]

		if !wasIndexed || cachedMtime != mtime {
			// New or modified file not tracked by MarkDirty.
			m.mu.Lock()
			m.dirtyFiles[relPath] = mtime
			m.mu.Unlock()
			newDirty++
		}
		return nil
	})

	// Check for deleted files (in index but not on disk).
	for p := range indexedPaths {
		if !found[p] {
			m.mu.Lock()
			m.dirtyFiles[p] = -1 // -1 signals deletion
			m.mu.Unlock()
			newDirty++
		}
	}

	if newDirty > 0 {
		debug.Log("codeindex", "periodic scan detected %d externally-changed files", newDirty)
		// Also update lastActivity since the workspace is being actively modified.
		m.mu.Lock()
		m.lastActivity = time.Now()
		m.mu.Unlock()
	}
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

	// #1317-A: Search/FilePathFuzzy grab the index pointer under RLock and
	// then read idx.df without any lock. Mutating the shared index in place
	// below raced those readers — concurrent map read/write is a fatal,
	// unrecoverable process crash. The old comment claimed "we'll mutate a
	// copy" but no copy existed. Clone the shell (docs slice + df map);
	// individual doc tf maps are never mutated (replacement assigns whole
	// docs), so a shallow element copy suffices. Readers keep the old,
	// now-immutable pointer; m.index swap below is the only publish point.
	clone := &bm25Index{
		docs:      make([]bm25Doc, len(idx.docs)),
		df:        make(map[string]int, len(idx.df)),
		avgLength: idx.avgLength,
	}
	copy(clone.docs, idx.docs)
	for t, n := range idx.df {
		clone.df[t] = n
	}
	idx = clone

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
				// #1317-B: removeDocFromIndex swaps the tail doc into slot
				// di — the tail doc's docIdx entry is now stale. Without
				// this fix a later replaceDocInIndex(idx, staleIdx, ...)
				// wrote past the truncated slice (panic in this unrecovered
				// background goroutine = process crash) or replaced the
				// wrong doc (silent index corruption).
				if di < len(idx.docs) {
					docIdx[idx.docs[di].path] = di
				}
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
	m.lastActivity = time.Now()
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
	type scored struct {
		path  string
		score int
	}
	var results []scored
	for _, doc := range idx.docs {
		lowerPath := strings.ToLower(doc.path)
		score := fuzzyScore(lowerPath, query)
		if score > 0 {
			results = append(results, scored{path: doc.path, score: score})
		}
	}
	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.path
	}
	return out
}

// fuzzyScore returns a relevance score for how well query matches path.
// Higher = better match. Returns 0 if no match.
// Scoring priorities:
//   - Contiguous basename match (e.g. "wechat" in "wechat.go") = highest
//   - Contiguous path match = high
//   - Shorter path = higher (fewer noise segments)
//   - Non-contiguous subsequence match = lower
func fuzzyScore(path, query string) int {
	if query == "" {
		return 1
	}
	if !fuzzySubsequenceMatch(path, query) {
		return 0
	}
	score := 0
	// Check contiguous match in basename
	basename := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		basename = path[idx+1:]
	}
	if strings.Contains(basename, query) {
		score += 1000
		// Exact basename match (e.g. query == basename without extension)
		baseNoExt := basename
		if dot := strings.LastIndex(baseNoExt, "."); dot > 0 {
			baseNoExt = baseNoExt[:dot]
		}
		if baseNoExt == query {
			score += 500
		}
	}
	// Check contiguous match in full path
	if strings.Contains(path, query) {
		score += 500
	}
	// Prefer shorter paths (fewer directory segments)
	dirs := strings.Count(path, "/")
	score -= dirs * 10
	// Base score for subsequence match
	score += 100
	if score < 1 {
		score = 1
	}
	return score
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
	m.lastActivity = time.Now()
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
