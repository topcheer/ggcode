package memory

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

type AutoMemory struct {
	dir string

	projectRoot string

	mu sync.Mutex

	useOnce sync.Map

	eventScope string
	eventSink  chan<- ChangeEvent
}

var writeMu sync.Map

func NewAutoMemory() *AutoMemory {
	home := config.HomeDir()
	dir := filepath.Join(home, ".ggcode", "memory")
	_ = os.MkdirAll(dir, 0755)
	return &AutoMemory{dir: dir}
}

// NewAutoMemoryAt builds an AutoMemory rooted at an explicit directory
// (tests, sweeps over isolated stores).
func NewAutoMemoryAt(dir string) *AutoMemory {
	_ = os.MkdirAll(dir, 0755)
	return &AutoMemory{dir: dir}
}

func NewProjectAutoMemory(workingDir string) *AutoMemory {
	home := config.HomeDir()

	if strings.EqualFold(workingDir, home) {
		return nil
	}
	dir := filepath.Join(workingDir, ".ggcode", "memory")
	_ = os.MkdirAll(dir, 0755)
	return &AutoMemory{dir: dir, projectRoot: workingDir}
}

func (am *AutoMemory) SaveMemory(key, content string) error {
	return am.SaveMemoryWithSource(key, content, "save_memory")
}

func (am *AutoMemory) SaveMemoryWithSource(key, content, source string) error {
	return am.SaveMemoryWithSourceActor(key, content, source, "")
}

func (am *AutoMemory) SaveMemoryWithSourceActor(key, content, source, actor string) error {

	safe := disambiguateKey(key, sanitizeKey(key))
	path := filepath.Join(am.dir, safe+".md")

	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {

		debug.Log("memory", "automemory filelock failed, degraded to unlocked write: %v", err)
	}

	tmp, err := os.CreateTemp(am.dir, safe+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write([]byte(content)); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}

	muAny, _ := writeMu.LoadOrStore(path, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	archived, archErr := am.archiveVersion(safe, path)
	if archErr != nil {
		debug.Log("memory", "history archive failed for %q (continuing overwrite): %v", safe, archErr)
	}
	if err := os.Rename(tmpName, path); err != nil {

		if archived != "" {
			if rbErr := os.Rename(archived, path); rbErr != nil {
				debug.Log("memory", "history rollback failed for %q (live file lost, archived copy at %s): %v", safe, archived, rbErr)
			}
		}
		os.Remove(tmpName)
		return err
	}
	am.RecordProvenanceActor(safe, source, actor)

	if pat := DetectInjectionTaint(key, content); pat != "" {
		if err := am.MarkTainted(safe, pat); err != nil {
			debug.Log("memory", "taint sidecar write failed for %q: %v", safe, err)
		}
	} else {
		am.ClearTaint(safe)
	}
	am.emitChange("save", safe, source)
	return nil
}

func (am *AutoMemory) LoadKey(key string) (string, error) {
	safe := disambiguateKey(key, sanitizeKey(key))
	path := filepath.Join(am.dir, safe+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (am *AutoMemory) LoadIndex() (string, []string, error) {
	metas, err := am.collectMetas()
	if err != nil {
		return "", nil, err
	}
	now := time.Now()
	active, expired, deduped, capped := curateEntries(metas, now)
	debug.Log("memory", "%s", formatMemorySummary(len(metas), len(active), expired, deduped, capped))

	var keys, files []string
	for _, m := range active {
		keys = append(keys, m.Key)
		files = append(files, filepath.Join(am.dir, m.Key+".md"))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	var builder strings.Builder
	for _, key := range keys {
		line := fmt.Sprintf("- %s", key)
		if info, ok := am.UsageOf(key); ok {
			line += provenanceSuffix(info)
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	am.RecordUse(keys, "index")
	return strings.TrimSpace(builder.String()), files, nil
}

func (am *AutoMemory) LoadAll() (string, []string, error) {
	metas, err := am.collectMetas()
	if err != nil {
		return "", nil, err
	}
	now := time.Now()
	active, expired, deduped, capped := curateEntries(metas, now)
	debug.Log("memory", "%s", formatMemorySummary(len(metas), len(active), expired, deduped, capped))

	var files []string
	var builder strings.Builder
	for _, m := range active {
		path := filepath.Join(am.dir, m.Key+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		builder.WriteString(fmt.Sprintf("### %s\n%s\n\n", m.Key, string(data)))
		files = append(files, path)
	}

	return strings.TrimSpace(builder.String()), files, nil
}

func (am *AutoMemory) List() ([]string, error) {
	entries, err := os.ReadDir(am.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var keys []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		keys = append(keys, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(keys)
	return keys, nil
}

func (am *AutoMemory) collectMetas() ([]MemoryMeta, error) {
	entries, err := os.ReadDir(am.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	usage := am.loadUsage()
	var metas []MemoryMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".md")
		meta := buildMemoryMeta(key, info.ModTime())
		if rec, ok := usage.Entries[key]; ok && rec != nil {
			meta.Uses = rec.Uses
			meta.LastUsedAt = rec.LastUsed
			meta.Source = rec.Source
			meta.Outcome = rec.Outcome
			meta.Conflicts = rec.Conflicts
		}
		metas = append(metas, meta)
	}
	return metas, nil
}

type MemoryEntryInfo struct {
	Key       string
	SizeBytes int64
	Uses      int
	LastUsed  time.Time
	Preview   string
	// UpdatedAt is the entry file's modification time, i.e. when the entry
	// content was last written.
	UpdatedAt time.Time
}

// Metas exposes full entry metadata (category/created/uses/source/outcome)
// for adjudication layers (curator sweep). Read-only snapshot.
func (am *AutoMemory) Metas() ([]MemoryMeta, error) {
	return am.collectMetas()
}

func (am *AutoMemory) ListDetailed() ([]MemoryEntryInfo, error) {
	metas, err := am.collectMetas()
	if err != nil {
		return nil, err
	}
	var out []MemoryEntryInfo
	for _, m := range metas {
		info := MemoryEntryInfo{Key: m.Key, Uses: m.Uses, LastUsed: m.LastUsedAt}
		if st, err := os.Stat(filepath.Join(am.dir, m.Key+".md")); err == nil {
			info.SizeBytes = st.Size()
			info.UpdatedAt = st.ModTime()
		}
		if data, err := os.ReadFile(filepath.Join(am.dir, m.Key+".md")); err == nil {
			info.Preview = previewLine(string(data), 160)
		}
		out = append(out, info)
	}
	return out, nil
}

func previewLine(body string, max int) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		runes := []rune(line)
		if len(runes) > max {
			runes = runes[:max]
		}
		return string(runes)
	}
	return ""
}

func (am *AutoMemory) Clear() error {
	entries, err := os.ReadDir(am.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(am.dir, e.Name()))
	}
	return nil
}

func (am *AutoMemory) Dir() string {
	return am.dir
}

const maxInlineBytes = 1200

const maxTotalInlineBytes = 6000

type MemoryEntry struct {
	Key     string
	Content string
	Meta    MemoryMeta
}

func (am *AutoMemory) LoadForPrompt() (inline []MemoryEntry, indexOnly []string, err error) {
	return am.loadForPrompt(true, "")
}

func (am *AutoMemory) LoadForPromptForTask(task string) (inline []MemoryEntry, indexOnly []string, err error) {
	return am.loadForPrompt(true, task)
}

func (am *AutoMemory) loadForPrompt(record bool, task string) (inline []MemoryEntry, indexOnly []string, err error) {
	metas, err := am.collectMetas()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()

	if sup := am.SupersededSet(); len(sup) > 0 {
		filtered := make([]MemoryMeta, 0, len(metas))
		for _, m := range metas {
			if sup[m.Key] {
				continue
			}
			filtered = append(filtered, m)
		}
		metas = filtered
	}

	if fg := am.ForgottenSet(); len(fg) > 0 {
		filtered := make([]MemoryMeta, 0, len(metas))
		for _, m := range metas {
			if fg[m.Key] {
				continue
			}
			filtered = append(filtered, m)
		}
		metas = filtered
	}

	active, _, _, _ := curateEntries(metas, now)

	sort.Slice(active, func(i, j int) bool {
		if active[i].Category == CategoryPersistent && active[j].Category != CategoryPersistent {
			return true
		}
		if active[i].Category != CategoryPersistent && active[j].Category == CategoryPersistent {
			return false
		}
		return active[i].Key < active[j].Key
	})

	totalInline := 0

	totalBudget := am.EffectiveInlineBudget()

	var relGate *relevanceGate
	if task != "" {
		var cands []relCandidate
		for _, m := range active {
			if m.Category != CategoryPersistent {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(am.dir, m.Key+".md")); err == nil {
				cands = append(cands, relCandidate{key: m.Key, content: string(b)})
			}
		}
		relGate = newRelevanceGate(task, cands)
	}
	for _, m := range active {

		if pat, tainted := am.TaintOf(m.Key); tainted {
			indexOnly = append(indexOnly, m.Key+" [tainted: "+pat+"]")
			debug.Log("memory", "skipping inline of tainted entry %q (pattern %q)", m.Key, pat)
			continue
		}
		path := filepath.Join(am.dir, m.Key+".md")
		data, readErr := os.ReadFile(path)
		content := ""
		if readErr == nil {
			content = strings.TrimSpace(string(data))
		}

		if pat := DetectInjectionTaint(m.Key, content); pat != "" {
			indexOnly = append(indexOnly, m.Key+" [tainted: "+pat+"]")
			debug.Log("memory", "read-time backstop tainted entry %q (pattern %q)", m.Key, pat)
			if err := am.MarkTainted(m.Key, pat); err != nil {
				debug.Log("memory", "backfill sidecar failed for %q: %v (guarded this startup only)", m.Key, err)
			}
			continue
		}

		if m.Category == CategoryPersistent && len(content) > 0 && len(content) <= maxInlineBytes && totalInline+len(content) <= totalBudget && relGate.relevant(m.Key, content) {
			inline = append(inline, MemoryEntry{
				Key:     m.Key,
				Content: content,
				Meta:    m,
			})
			totalInline += len(content)
		} else {
			indexOnly = append(indexOnly, m.Key)
		}
	}

	arb := ArbitrateInline(inline, now)
	if arb.HasConflicts() {
		arb.Annotate(inline)
		debug.Log("memory", "recall arbitration: %d conflict(s) among %d inline entries", len(arb.Conflicts), len(inline))
	}

	am.annotateStaleInline(inline)

	if record {

		var losers []string
		for _, c := range arb.Conflicts {
			if c.WinnerTrust-c.LoserTrust >= recallConflictLossGap {
				losers = append(losers, c.LoserKey)
			}
		}
		am.RecordConflictLoss(losers)
		inlineKeys := make([]string, 0, len(inline))
		for _, e := range inline {
			inlineKeys = append(inlineKeys, e.Key)
		}
		am.RecordUse(inlineKeys, "inline")
		am.RecordUse(indexOnly, "index")
	}

	return inline, indexOnly, nil
}

func sanitizeKey(key string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, key)

	for strings.Contains(safe, "--") {
		safe = strings.ReplaceAll(safe, "--", "-")
	}
	return strings.Trim(safe, "-")
}

func disambiguateKey(key, safe string) string {
	if safe == "" {

		safe = "untitled"
	}

	if safe == key {
		return safe
	}
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s-%x", safe, sum[:4])
}
