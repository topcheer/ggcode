package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

type CodeIndexManager struct {
	mu             sync.RWMutex
	index          *bm25Index
	ready          bool
	building       bool
	started        bool
	degraded       bool
	disabled       bool
	lastActivity   time.Time
	dirtyFiles     map[string]int64
	budgetExcluded map[string]int64
	indexPath      string
	workingDir     string
	stopCh         chan struct{}
	stopOnce       sync.Once
	rebuildCh      chan struct{}
	lockFile       *os.File
	onReady        func(stats CodeIndexStats)
	onProgress     func(done, total int)

	stats CodeIndexStats
}

type CodeIndexStats struct {
	TotalFiles   int       `json:"total_files"`
	IndexedFiles int       `json:"indexed_files"`
	IndexSize    int       `json:"index_size"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type persistedDoc struct {
	Path   string         `json:"path"`
	Mtime  int64          `json:"mtime"`
	TF     map[string]int `json:"tf"`
	Length int            `json:"length"`
}

type persistedExcluded struct {
	Path  string `json:"path"`
	Mtime int64  `json:"mtime"`
}

type persistedIndex struct {
	Version    int                 `json:"version"`
	WorkingDir string              `json:"working_dir"`
	UpdatedAt  time.Time           `json:"updated_at"`
	Docs       []persistedDoc      `json:"docs"`
	Excluded   []persistedExcluded `json:"excluded,omitempty"`
}

const codeIndexVersion = 1

const (
	codeIndexMaxFiles     = 50000
	codeIndexMaxFileSize  = 256 * 1024
	codeIndexBuildTimeout = 5 * time.Minute
	codeIndexRebuildTick  = 5 * time.Minute

	codeIndexMaxTotalTerms = 1_500_000

	codeIndexRebuildDebounce = 3 * time.Second
)

const codeIndexIdleRelease = 60 * time.Minute

func NewCodeIndexManager(workingDir string) *CodeIndexManager {
	m := &CodeIndexManager{
		workingDir: workingDir,
		dirtyFiles: make(map[string]int64),
		stopCh:     make(chan struct{}),
		rebuildCh:  make(chan struct{}, 1),
	}

	if isHomeDirectory(workingDir) {
		m.disabled = true
		debug.Log("codeindex", "indexing disabled: working directory is the user home")
	}
	m.indexPath = m.computeIndexPath()
	return m
}

func isHomeDirectory(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	return sameDirPath(dir, home)
}

func sameDirPath(a, b string) bool {
	norm := func(p string) string {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		clean := filepath.Clean(abs)
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			return filepath.Clean(resolved)
		}
		return clean
	}
	na, nb := norm(a), norm(b)
	if na == nb {
		return true
	}

	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(na, nb)
	}
	return false
}

func (m *CodeIndexManager) computeIndexPath() string {
	abs, err := filepath.Abs(m.workingDir)
	if err != nil {
		abs = m.workingDir
	}
	h := sha256.Sum256([]byte(abs))
	hash := hex.EncodeToString(h[:8])
	return filepath.Join(config.ConfigDir(), "cache", "codeindex", hash+".json")
}

func (m *CodeIndexManager) StartBackgroundIndex() {
	m.mu.Lock()
	if m.disabled {

		m.mu.Unlock()
		return
	}
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

		if !m.tryLock() {
			debug.Log("codeindex", "another instance is building the index, degrading to read-only reload loop")

			m.loadDiskCache()

			select {
			case <-m.rebuildCh:
			default:
			}
			m.mu.Lock()
			m.degraded = true
			m.mu.Unlock()
			safego.Go("codeindex.dirtycheck", m.backgroundLoop)
			return
		}
		defer m.unlock()

		m.doBuild(ctx)

		safego.Go("codeindex.dirtycheck", m.backgroundLoop)
	})
}

func (m *CodeIndexManager) tryLock() bool {
	lockPath := m.indexPath + ".lock"

	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return false
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		debug.Log("codeindex", "lock: open error: %v", err)
		return false
	}

	if !lockFileExcl(f) {
		_ = f.Close()
		return false
	}

	m.mu.Lock()
	m.lockFile = f
	m.mu.Unlock()
	return true
}

func (m *CodeIndexManager) unlock() {
	m.mu.Lock()
	f := m.lockFile
	m.lockFile = nil
	m.mu.Unlock()
	if f != nil {
		unlockFileExcl(f)
		_ = f.Close()
	}
}

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
	var excluded map[string]int64
	if len(pi.Excluded) > 0 {
		excluded = make(map[string]int64, len(pi.Excluded))
		for _, e := range pi.Excluded {
			excluded[e.Path] = e.Mtime
		}
	}
	var totalLength int
	for _, d := range pi.Docs {
		if len(d.TF) == 0 {
			continue
		}
		doc := bm25Doc{path: d.Path, tf: d.TF, length: d.Length, mtime: d.Mtime}
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
	if excluded != nil {
		m.budgetExcluded = excluded
	}
	m.stats = CodeIndexStats{
		TotalFiles:   len(idx.docs),
		IndexedFiles: len(idx.docs),
		UpdatedAt:    pi.UpdatedAt,
	}
	m.mu.Unlock()
	debug.Log("codeindex", "loaded %d docs from disk cache (locked by another instance)", len(idx.docs))
}

func (m *CodeIndexManager) doBuild(ctx context.Context) {
	start := time.Now()

	overflow := make(map[string]int64)
	var cached []persistedDoc
	if data, err := os.ReadFile(m.indexPath); err == nil {
		var pi persistedIndex
		if json.Unmarshal(data, &pi) == nil && pi.Version == codeIndexVersion {
			cached = pi.Docs
			debug.Log("codeindex", "loaded %d docs from disk cache", len(cached))
		}
	}

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

	files := m.collectFiles(ctx)
	if len(files) == 0 {
		debug.Log("codeindex", "no indexable files found in %s", m.workingDir)
		return
	}

	var docs []bm25Doc
	var totalLength int
	var totalTerms int
	truncated := false
	skipped, indexed := 0, 0
	maxTerms := codeIndexMaxTotalTermsOverride()

	m.mu.RLock()
	progressFn := m.onProgress
	m.mu.RUnlock()
	lastProgress := time.Now()

	breakIdx := -1
	for i, absPath := range files {
		if maxTerms > 0 && totalTerms >= maxTerms {
			truncated = true

			breakIdx = i - 1
			break
		}
		if ctx.Err() != nil {

			debug.Log("codeindex", "build timed out after %d files - publishing the indexed subset", indexed)
			truncated = true

			breakIdx = i - 1
			break
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

		if cached, ok := cachedMap[relPath]; ok && cached.Mtime == mtime {
			if len(cached.TF) > 0 {
				if maxTerms > 0 && totalTerms+len(cached.TF) > maxTerms {
					truncated = true
					overflow[relPath] = mtime
					breakIdx = i
					break
				}
				docs = append(docs, bm25Doc{
					path:   relPath,
					tf:     cached.TF,
					length: cached.Length,
					mtime:  mtime,
				})
				totalTerms += len(cached.TF)
				totalLength += cached.Length
				indexed++
				continue
			}
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			skipped++
			continue
		}
		terms := tokenizeForIndex(string(data))
		if len(terms) == 0 {
			continue
		}
		tf := make(map[string]int, len(terms))
		for _, term := range terms {
			tf[term]++
		}
		if maxTerms > 0 && totalTerms+len(tf) > maxTerms {
			truncated = true
			overflow[relPath] = mtime
			breakIdx = i
			break
		}
		docs = append(docs, bm25Doc{
			path:   relPath,
			tf:     tf,
			length: len(terms),
			mtime:  mtime,
		})
		totalTerms += len(tf)
		totalLength += len(terms)
		indexed++
		if progressFn != nil && time.Since(lastProgress) >= 400*time.Millisecond {
			lastProgress = time.Now()
			progressFn(indexed, len(files))
		}
	}
	if truncated {
		debug.Log("codeindex", "term budget reached (%d terms over %d docs) - index truncated at %d of %d files; "+
			"set GGCODE_CODE_INDEX_MAX_TERMS to adjust, or start ggcode inside a single project",
			totalTerms, len(docs), len(docs), len(files))

		for _, p := range files[breakIdx+1:] {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				if rel, err := filepath.Rel(m.workingDir, p); err == nil {
					overflow[rel] = info.ModTime().Unix()
				}
			}
		}
	}

	if len(docs) == 0 {
		debug.Log("codeindex", "no documents indexed after walk")
		return
	}

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

	m.persistIndex(ctx, docs, overflow)

	m.mu.Lock()
	m.index = idx
	m.budgetExcluded = overflow
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

func (m *CodeIndexManager) SetOnReady(fn func(stats CodeIndexStats)) {
	m.mu.Lock()
	m.onReady = fn

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

func (m *CodeIndexManager) SetOnProgress(fn func(done, total int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onProgress = fn
}

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

func isInSkipDir(relPath string) bool {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, p := range parts {
		if isSkipDir(p) {
			return true
		}
	}
	return false
}

func isSkipDir(name string) bool {
	switch name {

	case ".git", ".svn", ".hg", ".bzr", ".ggcode", ".idea", ".vscode":
		return true

	case "node_modules", "bower_components", ".next", ".nuxt", ".svelte-kit",
		".turbo", ".parcel-cache", "coverage", ".cypress":
		return true

	case "__pycache__", ".venv", "venv", ".tox", ".mypy_cache", ".pytest_cache",
		".ruff_cache", "site-packages", "eggs", ".eggs":
		return true

	case "target", ".gradle", ".maven", "build", "out", "vendor":
		return true

	case ".bundle", "tmp", ".husky", ".pnp", ".yarn":
		return true

	case "cmake-build-debug", "cmake-build-release":
		return true

	case "DerivedData", ".build":
		return true

	case "dist", "bin", "obj", ".cache", "temp", ".tmp":
		return true
	}
	return false
}

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

func (m *CodeIndexManager) persistIndex(ctx context.Context, docs []bm25Doc, excluded map[string]int64) {
	pi := persistedIndex{
		Version:    codeIndexVersion,
		WorkingDir: m.workingDir,
		UpdatedAt:  time.Now(),
		Docs:       make([]persistedDoc, 0, len(docs)),
	}

	if len(excluded) > 0 {
		pi.Excluded = make([]persistedExcluded, 0, len(excluded))
		for p, mt := range excluded {
			pi.Excluded = append(pi.Excluded, persistedExcluded{Path: p, Mtime: mt})
		}
	}

	for _, doc := range docs {
		pi.Docs = append(pi.Docs, persistedDoc{
			Path:   doc.path,
			Mtime:  doc.mtime,
			TF:     doc.tf,
			Length: doc.length,
		})
	}

	dir := filepath.Dir(m.indexPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		debug.Log("codeindex", "persist: mkdir error: %v", err)
		return
	}

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

func codeIndexMaxTotalTermsOverride() int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("GGCODE_CODE_INDEX_MAX_TERMS")))
	if err != nil {
		return codeIndexMaxTotalTerms
	}
	if v < 0 {
		return 0
	}
	return v
}

func (m *CodeIndexManager) backgroundLoop() {
	ticker := time.NewTicker(codeIndexRebuildTick)
	defer ticker.Stop()

	var debounceTimer *time.Timer

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.periodicCheck()
		case <-m.rebuildCh:

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

func (m *CodeIndexManager) periodicCheck() {

	m.mu.RLock()
	degraded := m.degraded
	stillReady := m.ready
	m.mu.RUnlock()
	if degraded && stillReady {

		m.loadDiskCache()
	}

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

	m.scanForExternalChanges()

	m.rebuildDirty("periodic")
}

func (m *CodeIndexManager) scanForExternalChanges() {
	m.mu.RLock()
	idx := m.index
	m.mu.RUnlock()
	if idx == nil {
		return
	}

	indexedPaths := make(map[string]bool, len(idx.docs))
	for _, doc := range idx.docs {
		indexedPaths[doc.path] = true
	}

	var cachedMap map[string]int64
	excludedMap := make(map[string]int64)
	if data, err := os.ReadFile(m.indexPath); err == nil {
		var pi persistedIndex
		if json.Unmarshal(data, &pi) == nil && pi.Version == codeIndexVersion {
			cachedMap = make(map[string]int64, len(pi.Docs))
			for _, d := range pi.Docs {
				cachedMap[d.Path] = d.Mtime
			}
			for _, e := range pi.Excluded {
				excludedMap[e.Path] = e.Mtime
			}
		}
	}
	if cachedMap == nil {

		cachedMap = make(map[string]int64)
		debug.Log("codeindex", "disk cache unavailable, external scan marks all for one full rebuild (%d indexed paths)", len(indexedPaths))
	}

	found := make(map[string]bool)
	newDirty := 0

	walkTruncated := false

	_ = filepath.WalkDir(m.workingDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if isSkipDir(d.Name()) {
				return filepath.SkipDir
			}

			if path != m.workingDir {
				if _, statErr := os.Stat(filepath.Join(path, ".git")); statErr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !isCodeFile(path) {
			return nil
		}

		if len(found) >= codeIndexMaxFiles {
			walkTruncated = true
			return filepath.SkipAll
		}
		relPath, _ := filepath.Rel(m.workingDir, path)
		found[relPath] = true

		info, err := os.Stat(path)
		if err != nil || info.Size() > codeIndexMaxFileSize {
			return nil
		}
		mtime := info.ModTime().Unix()
		cachedMtime, wasIndexed := cachedMap[relPath]
		_, isExcluded := excludedMap[relPath]

		if !isExcluded && (!wasIndexed || cachedMtime != mtime) {

			m.mu.Lock()
			m.dirtyFiles[relPath] = mtime
			m.mu.Unlock()
			newDirty++
		}
		return nil
	})

	if walkTruncated {
		debug.Log("codeindex", "periodic scan truncated at file cap - deletion detection skipped this cycle (#4548)")
	} else {
		for p := range indexedPaths {
			if !found[p] {
				m.mu.Lock()
				m.dirtyFiles[p] = -1
				m.mu.Unlock()
				newDirty++
			}
		}
	}

	if newDirty > 0 {
		debug.Log("codeindex", "periodic scan detected %d externally-changed files", newDirty)

		m.mu.Lock()
		m.lastActivity = time.Now()
		m.mu.Unlock()
	}
}

func (m *CodeIndexManager) rebuildDirty(reason string) {
	m.mu.RLock()
	dirtyCount := len(m.dirtyFiles)
	ready := m.ready
	m.mu.RUnlock()
	if dirtyCount == 0 || !ready {
		return
	}

	if !m.tryLock() {
		debug.Log("codeindex", "%s rebuild: lock held by another instance, retaining dirty set for next cycle", reason)

		return
	}

	m.mu.Lock()
	wasDegraded := m.degraded
	m.degraded = false
	m.mu.Unlock()
	if wasDegraded {
		debug.Log("codeindex", "degraded instance acquired the lock, promoted to writer")
	}

	m.mu.Lock()
	dirty := make(map[string]int64, len(m.dirtyFiles))
	for k, v := range m.dirtyFiles {
		if _, ex := m.budgetExcluded[k]; ex {
			continue
		}
		dirty[k] = v
	}
	m.dirtyFiles = make(map[string]int64)
	idx := m.index
	m.mu.Unlock()

	if idx == nil {
		debug.Log("codeindex", "%s rebuild: index nil, falling back to full build", reason)
		ctx, cancel := context.WithTimeout(context.Background(), codeIndexBuildTimeout)
		m.doBuild(ctx)
		cancel()
		m.unlock()
		return
	}

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

	totalTerms := 0
	for _, d := range idx.docs {
		totalTerms += len(d.tf)
	}
	maxTerms := codeIndexMaxTotalTermsOverride()

	debug.Log("codeindex", "%s incremental: %d files changed", reason, len(dirty))
	start := time.Now()

	docIdx := make(map[string]int, len(idx.docs))
	for i, d := range idx.docs {
		docIdx[d.path] = i
	}

	updated, added, removed := 0, 0, 0
	for dirtyPath := range dirty {

		relPath := dirtyPath
		if filepath.IsAbs(dirtyPath) {
			if rp, err := filepath.Rel(m.workingDir, dirtyPath); err == nil {
				relPath = rp
			}
		}
		absPath := filepath.Join(m.workingDir, relPath)
		info, err := os.Stat(absPath)
		if err != nil {

			if !os.IsNotExist(err) {
				debug.Log("codeindex", "incremental stat error (keeping doc): %s: %v", relPath, err)
				continue
			}

			if di, ok := docIdx[relPath]; ok {
				m.removeDocFromIndex(idx, di)
				delete(docIdx, relPath)

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

		if isInSkipDir(relPath) {
			continue
		}

		if !isCodeFile(absPath) {
			continue
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}
		terms := tokenizeForIndex(string(data))

		if len(terms) == 0 {

			if di, ok := docIdx[relPath]; ok {
				m.removeDocFromIndex(idx, di)
				delete(docIdx, relPath)
				if di < len(idx.docs) {
					docIdx[idx.docs[di].path] = di
				}
				removed++
			}
			continue
		}
		newTF := make(map[string]int, len(terms))
		for _, t := range terms {
			newTF[t]++
		}
		newDoc := bm25Doc{path: relPath, tf: newTF, length: len(terms), mtime: info.ModTime().Unix()}

		if i, ok := docIdx[relPath]; ok {

			if maxTerms > 0 {
				delta := len(newTF) - len(idx.docs[i].tf)
				if totalTerms+delta > maxTerms {
					debug.Log("codeindex", "incremental replace skipped, term budget reached: %s", relPath)
					continue
				}
			}
			totalTerms += len(newTF) - len(idx.docs[i].tf)
			m.replaceDocInIndex(idx, i, newDoc)
			updated++
		} else {

			if len(idx.docs) >= codeIndexMaxFiles {
				debug.Log("codeindex", "incremental append skipped, file cap reached: %s", relPath)
				continue
			}
			if maxTerms > 0 && totalTerms+len(newTF) > maxTerms {

				debug.Log("codeindex", "incremental append skipped, term budget reached: %s", relPath)
				continue
			}
			totalTerms += len(newTF)
			idx.docs = append(idx.docs, newDoc)
			docIdx[relPath] = len(idx.docs) - 1
			for term := range newTF {
				idx.df[term]++
			}
			added++
		}
	}

	totalLen := 0
	for _, d := range idx.docs {
		totalLen += d.length
	}
	if len(idx.docs) > 0 {
		idx.avgLength = float64(totalLen) / float64(len(idx.docs))
	}

	m.mu.RLock()
	snapshotExcluded := m.budgetExcluded
	m.mu.RUnlock()
	m.persistIndex(context.Background(), idx.docs, snapshotExcluded)

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

func (m *CodeIndexManager) removeDocFromIndex(idx *bm25Index, i int) {
	doc := idx.docs[i]

	for term := range doc.tf {
		idx.df[term]--
		if idx.df[term] <= 0 {
			delete(idx.df, term)
		}
	}

	last := len(idx.docs) - 1
	idx.docs[i] = idx.docs[last]
	idx.docs = idx.docs[:last]
}

func (m *CodeIndexManager) replaceDocInIndex(idx *bm25Index, i int, newDoc bm25Doc) {
	oldDoc := idx.docs[i]

	for term := range oldDoc.tf {
		idx.df[term]--
		if idx.df[term] <= 0 {
			delete(idx.df, term)
		}
	}

	for term := range newDoc.tf {
		idx.df[term]++
	}
	idx.docs[i] = newDoc
}

func (m *CodeIndexManager) inNestedRepo(path string) bool {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(m.workingDir, abs)
	}
	abs = filepath.Clean(abs)
	wd := filepath.Clean(m.workingDir)
	if abs == wd || !strings.HasPrefix(abs, wd+string(filepath.Separator)) {
		return abs != wd
	}
	for dir := filepath.Dir(abs); dir != wd && dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
	}
	return false
}

func (m *CodeIndexManager) MarkDirty(paths []string) {
	m.mu.Lock()
	for _, p := range paths {

		if m.inNestedRepo(p) {
			continue
		}
		m.dirtyFiles[p] = time.Now().Unix()
	}
	m.lastActivity = time.Now()
	ready := m.ready
	started := m.started
	m.mu.Unlock()

	if ready && started {
		select {
		case m.rebuildCh <- struct{}{}:
		default:
		}
	}
}

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

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if maxResults <= 0 {
		maxResults = 10
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.path
	}
	return out
}

func fuzzyScore(path, query string) int {
	if query == "" {
		return 1
	}
	if !fuzzySubsequenceMatch(path, query) {
		return 0
	}
	score := 0

	basename := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		basename = path[idx+1:]
	}
	if strings.Contains(basename, query) {
		score += 1000

		baseNoExt := basename
		if dot := strings.LastIndex(baseNoExt, "."); dot > 0 {
			baseNoExt = baseNoExt[:dot]
		}
		if baseNoExt == query {
			score += 500
		}
	}

	if strings.Contains(path, query) {
		score += 500
	}

	dirs := strings.Count(path, "/")
	score -= dirs * 10

	score += 100
	if score < 1 {
		score = 1
	}
	return score
}

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

		if !building {
			m.lazyLoad()
		}
		m.mu.RLock()
		disabled := m.disabled
		m.mu.RUnlock()
		if disabled {
			return nil, errIndexDisabled
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

func (m *CodeIndexManager) IsReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

func (m *CodeIndexManager) Stats() CodeIndexStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

func (m *CodeIndexManager) Stop() {

	m.stopOnce.Do(func() { close(m.stopCh) })
}

func (m *CodeIndexManager) lazyLoad() {
	m.mu.Lock()
	if m.disabled || m.building || m.ready {
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

var errIndexNotReady = &indexNotReadyError{}

type indexNotReadyError struct{}

func (e *indexNotReadyError) Error() string {
	return "code index is being built in the background, please try again in a few seconds"
}

var errIndexDisabled = errors.New("code index is disabled for this working directory (user home - not a project tree); use grep or search_files with an explicit path under a real project instead")
