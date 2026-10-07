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

// AutoMemory manages automatic memory persistence in ~/.ggcode/memory/.
type AutoMemory struct {
	dir string
	// projectRoot is set only for project-scoped instances (NewProjectAutoMemory).
	// It anchors injection-time staleness checks (annotateStaleInline) so that
	// relative paths inside memory entries are resolved against the right
	// workspace. Global memory leaves it empty and the check degrades to a no-op
	// (HOME would be a meaningless anchor for repo-style paths).
	projectRoot string
	// #1752 case 2: Load/Merge/Save read-modify-write cycles from concurrent
	// goroutines (reflection, /reflect, daemon) raced and the later write
	// silently dropped the earlier one; a bare WriteFile also let a reader
	// see a torn file. A package-level per-path mutex serializes writers,
	// and writes go through temp+rename so readers never see partial files.
	mu sync.Mutex
	// useOnce debounces RecordUse per key per process (sa-85): prompt
	// refresh storms rebuild the index on every save; without the debounce
	// one burst would multiply usage counters.
	useOnce sync.Map
}

// writeMu serializes writes to the same memory FILE path across separate
// AutoMemory instances (global + project memory share nothing, but two
// project instances for the same dir can exist in one process).
var writeMu sync.Map // path -> *sync.Mutex

// NewAutoMemory creates an AutoMemory instance for global memory (~/.ggcode/memory/).
func NewAutoMemory() *AutoMemory {
	home := config.HomeDir()
	dir := filepath.Join(home, ".ggcode", "memory")
	_ = os.MkdirAll(dir, 0755)
	return &AutoMemory{dir: dir}
}

// NewProjectAutoMemory creates an AutoMemory instance for project-scoped memory.
// Uses <workingDir>/.ggcode/memory/ directly — no parent directory traversal.
// Returns nil only if workingDir is the user's HOME directory.
func NewProjectAutoMemory(workingDir string) *AutoMemory {
	home := config.HomeDir()
	// Never treat HOME as a project root to avoid polluting ~/.ggcode/
	if strings.EqualFold(workingDir, home) {
		return nil
	}
	dir := filepath.Join(workingDir, ".ggcode", "memory")
	_ = os.MkdirAll(dir, 0755)
	return &AutoMemory{dir: dir, projectRoot: workingDir}
}

// SaveMemory saves a memory entry to ~/.ggcode/memory/{key}.md.
func (am *AutoMemory) SaveMemory(key, content string) error {
	return am.SaveMemoryWithSource(key, content, "save_memory")
}

// SaveMemoryWithSource saves a memory entry and records its PROVENANCE
// (sa-85): which subsystem created it. The source label lands in the
// .usage.json sidecar on first write of the key and survives later
// overwrites, so every prompt-injected memory can be traced to its origin
// (arXiv:2608.29606 provenance-aware memory).
func (am *AutoMemory) SaveMemoryWithSource(key, content, source string) error {
	return am.SaveMemoryWithSourceActor(key, content, source, "")
}

// SaveMemoryWithSourceActor (r29) is SaveMemoryWithSource with the WRITER
// identity recorded alongside the source label (actor-aware provenance,
// completing the arXiv:2608.29606 actor dimension: sub-agents and swarm
// teammates share this AutoMemory, and without an actor field a
// sub-agent's overwrite of a main-agent memory left zero trace of who
// wrote it). Empty actor = legacy callers, byte-identical behavior.
func (am *AutoMemory) SaveMemoryWithSourceActor(key, content, source, actor string) error {
	// #775: sanitizeKey is not injective ("a/b"/"a.b"/"a b" all -> "a-b";
	// pure-CJK keys -> "" -> untitled.md, so ALL Chinese memories shared one
	// file and silently overwrote each other). disambiguateKey appends a short
	// stable hash for keys whose sanitization collides.
	safe := disambiguateKey(key, sanitizeKey(key))
	path := filepath.Join(am.dir, safe+".md")

	// r401 GAP-B: cross-process write safety. writeMu below only serializes
	// writers within ONE process; two ggcode instances (a seat process plus a
	// subagent process, or a daemon reflecting while a session saves) share
	// this dir and raced last-write-wins, with .usage.json sidecar updates
	// interleaving between the two. util.FileLock (flock / LockFileEx, shared
	// with auth store and knight) serializes across processes; on lock
	// failure we degrade to the previous single-process behavior rather than
	// blocking memory writes on lock-infrastructure faults (knight
	// semantic_memory.go precedent).
	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		// #3120 V4: fail-open is deliberate (memory writes must not block
		// on lock-infrastructure faults) but must be observable.
		debug.Log("memory", "automemory filelock failed, degraded to unlocked write: %v", err)
	}

	// #1752 case 2: atomic write - temp file in the same directory, then
	// rename (atomic on POSIX and Windows-NT). Concurrent readers see
	// either the old or the new content, never a torn file.
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
	// r488 non-destructive overwrite (Mem++, arXiv:2610.02002): archive the
	// outgoing version into .history/ before the rename below destroys it.
	// Fail-open per #3120 precedent - a save must not block on archive
	// faults - but the potential loss stays observable via debug.Log.
	if err := am.archiveVersion(safe, path); err != nil {
		debug.Log("memory", "history archive failed for %q (continuing overwrite): %v", safe, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	am.RecordProvenanceActor(safe, source, actor)

	// r409 memory-poisoning defense (MINJA arXiv 2601.05504, sleeper
	// poisoning arXiv 2605.15338): a poisoned entry persisted here would be
	// inlined raw into EVERY future system prompt via loadForPrompt - one
	// successful injection buys a persistent, unreviewed channel. Scan at the
	// AutoMemory layer so every writer (save_memory tool, run-reflection,
	// preference distill) is covered. Never blocks the save (a legitimate
	// security writeup must still persist) - it marks a quarantine sidecar
	// that loadForPrompt demotes to index-only. A clean re-save of the same
	// key clears the flag so a fixed entry recovers.
	if pat := DetectInjectionTaint(key, content); pat != "" {
		if err := am.MarkTainted(safe, pat); err != nil {
			debug.Log("memory", "taint sidecar write failed for %q: %v", safe, err)
		}
	} else {
		am.ClearTaint(safe)
	}
	return nil
}

// LoadKey reads a single memory key's content (#1388). LoadAll merges EVERY
// active key - callers that only own one key (e.g. reflection's
// run-insights) must not use it: the merged text re-enters their own key on
// save and cross-pollutes unrelated memories into every prompt injection.
// Returns ("", nil) when the key has no file yet (first write).
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

// LoadIndex loads all memory file keys and returns a formatted index (titles
// only) plus the list of file paths. Applies curation filtering (expiry +
// dedup) so the system prompt only shows active memories.
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

	// sa-85 provenance display + usage feedback: inject the compact
	// provenance marker for entries with recorded usage so the model can
	// weigh each memory's track record, and count this injection as one
	// use per key (debounced).
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

// LoadAll loads all memory files and returns their combined content.
// Applies curation filtering (expiry + dedup) so only active memories
// are injected into the LLM context.
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

// List returns all memory keys (unfiltered — includes expired/deduped).
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

// collectMetas reads the memory directory and returns MemoryMeta for each .md file.
// sa-85: each meta carries its recorded provenance/usage from the sidecar
// so consumers (HealthReport) can surface usage signals without re-reading.
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
		}
		metas = append(metas, meta)
	}
	return metas, nil
}

// MemoryEntryInfo is the read-only projection returned by ListDetailed:
// enough for the agent to decide which entries to re-save, supersede, or
// delete, without loading full contents.
type MemoryEntryInfo struct {
	Key       string
	SizeBytes int64
	Uses      int
	LastUsed  time.Time
	Preview   string // first content line, markdown stripped, capped
}

// ListDetailed returns key/size/usage/preview for every stored entry
// (including expired/deduped ones — visibility first, the agent judges).
// This is the read side of the memory lifecycle: save_memory writes,
// delete_memory removes, but until now the agent had no way to SEE what
// is stored, so outdated entries persisted silently (SelfMem gap: agent
// as memory curator needs an inventory view).
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
		}
		if data, err := os.ReadFile(filepath.Join(am.dir, m.Key+".md")); err == nil {
			info.Preview = previewLine(string(data), 160)
		}
		out = append(out, info)
	}
	return out, nil
}

// previewLine returns the first non-heading, non-empty line of a memory
// body, capped to max runes.
func previewLine(body string, max int) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // skip blank and markdown heading lines
		}
		runes := []rune(line)
		if len(runes) > max {
			runes = runes[:max]
		}
		return string(runes)
	}
	return ""
}

// Clear removes all memory files.
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

// Dir returns the memory directory path.
func (am *AutoMemory) Dir() string {
	return am.dir
}

// maxInlineBytes is the per-entry size limit for inlining memory content
// directly into the system prompt. Entries larger than this are kept as
// title-only index entries. 1200 bytes ~ 300 tokens - enough for a concise
// build-process note or architecture decision, small enough to stay cheap.
const maxInlineBytes = 1200

// maxTotalInlineBytes is the combined size budget for all inlined memory
// entries. This prevents memory content from consuming too much of the
// context window. 6000 bytes ≈ 1500 tokens.
const maxTotalInlineBytes = 6000

// MemoryEntry pairs a curated memory's metadata with its file content.
type MemoryEntry struct {
	Key     string
	Content string
	Meta    MemoryMeta
}

// LoadForPrompt returns curated memory entries split into two groups:
//
//   - inline: entries whose full content should be injected directly into the
//     system prompt. These are persistent-category entries (architecture
//     decisions, build processes, design docs) that are small enough to fit
//     within the inline budget.
//   - indexOnly: entries whose titles should be listed as a reference index.
//     The LLM can read_file these when needed. This includes transient,
//     evolving, and oversized persistent entries.
//
// This implements relevance-free auto-injection: persistent knowledge from
// previous sessions is immediately available in context without requiring the
// LLM to manually read_file each memory entry.
//
// sa-85: each call records one prompt-injection "use" per injected key
// (inline entries count more strongly - their content was actually served).
// Diagnostics-only callers (HealthReport) use loadForPrompt(false) so a
// health check does not inflate the usage telemetry it reports.
func (am *AutoMemory) LoadForPrompt() (inline []MemoryEntry, indexOnly []string, err error) {
	return am.loadForPrompt(true, "")
}

// LoadForPromptForTask is LoadForPrompt with a task-relevance gate
// (sa-113, Self-RAG [IsRel] deterministic equivalent): when task is
// non-empty, persistent entries lexically unrelated to the task degrade
// from inline to index-only instead of occupying the system prompt.
// Callers that have a concrete task (sub-agent prompts) should prefer
// this; callers without one keep LoadForPrompt and the gate stays off.
func (am *AutoMemory) LoadForPromptForTask(task string) (inline []MemoryEntry, indexOnly []string, err error) {
	return am.loadForPrompt(true, task)
}

func (am *AutoMemory) loadForPrompt(record bool, task string) (inline []MemoryEntry, indexOnly []string, err error) {
	metas, err := am.collectMetas()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()

	// Superseded entries (memory evolution, supersede.go): retired from
	// prompt injection and recall arbitration, but kept on disk for history
	// (read_file still works). This also stops the ghost-context guard from
	// re-annotating a conflict that has already been structurally resolved.
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

	active, _, _, _ := curateEntries(metas, now)

	// Sort active entries: persistent first (inline priority), then by key
	// for deterministic output.
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
	// sa-113 task-relevance gate: pre-read persistent candidates once to
	// build the IDF statistics, then score each inline decision. Only the
	// persistent channel is gated - the index list always stays complete.
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
		// r409: quarantined entries (injection-pattern match at write time)
		// are never auto-inlined into the system prompt - the persistent
		// channel MINJA demonstrated. They stay index-only so the model can
		// still retrieve them via read_file, which runs through the
		// externalContentTools wrap (agent guard) - closed loop.
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

		// #3137: read-time content backstop for legacy stock and bypass
		// writes. The write-time scan (SaveMemoryWithSource) only covers
		// entries that went through THIS process's save path; entries
		// persisted before r409, or written by another instance / a plain
		// os.WriteFile, carry no .taint sidecar and would be inlined raw.
		// One pattern scan per inline candidate is negligible next to the
		// file read itself; a hit backfills the sidecar so the next startup
		// takes the fast TaintOf path.
		if pat := DetectInjectionTaint(m.Key, content); pat != "" {
			indexOnly = append(indexOnly, m.Key+" [tainted: "+pat+"]")
			debug.Log("memory", "read-time backstop tainted entry %q (pattern %q)", m.Key, pat)
			if err := am.MarkTainted(m.Key, pat); err != nil {
				debug.Log("memory", "backfill sidecar failed for %q: %v (guarded this startup only)", m.Key, err)
			}
			continue
		}

		// Inline persistent entries that are small enough and within budget.
		if m.Category == CategoryPersistent && len(content) > 0 && len(content) <= maxInlineBytes && totalInline+len(content) <= maxTotalInlineBytes && relGate.relevant(m.Key, content) {
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

	// Recall-time conflict arbitration ("ghost context" guard, sa-84):
	// write-time CheckContradiction cannot catch pairs that drifted apart
	// offline, so conflicting entries would otherwise be injected together
	// with no warning. Arbitrate among the inline set and annotate the
	// lower-trust side; winners stay clean. Annotation happens after
	// budget accounting and is capped by maxRecallConflicts.
	if arb := ArbitrateInline(inline, now); arb.HasConflicts() {
		arb.Annotate(inline)
		debug.Log("memory", "recall arbitration: %d conflict(s) among %d inline entries", len(arb.Conflicts), len(inline))
	}

	// Injection-time staleness annotation (STALE, arXiv:2605.06527 "Implicit
	// Conflict"): ArbitrateInline only fires when two conflicting entries are
	// inline simultaneously; a single entry whose referenced paths have since
	// disappeared (broken-path) would otherwise be injected as an unflagged
	// false premise. ScanStaleness feeds only the offline repair loop, so this
	// is the inline-side front line: deterministic, annotation-only, capped.
	am.annotateStaleInline(inline)

	if record {
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
	// Collapse consecutive dashes and trim
	for strings.Contains(safe, "--") {
		safe = strings.ReplaceAll(safe, "--", "-")
	}
	return strings.Trim(safe, "-")
}

// disambiguateKey makes the sanitized filename injective on the original
// key: clean ASCII keys keep their readable form; anything containing other
// characters (spaces, punctuation, CJK) gets a short stable hash suffix so
// distinct keys never map to the same file (#775).
func disambiguateKey(key, safe string) string {
	if safe == "" {
		// Pure non-ASCII key (e.g. Chinese): keep "untitled" readable base,
		// hash still separates different keys.
		safe = "untitled"
	}
	// #1279: injective means sanitize(key) == key VERBATIM. The old charset
	// loop passed keys like "a--b" or "-build-" as injective even though
	// sanitizeKey folds "--"→"-" and trims edges - so "a--b" and "a-b"
	// both landed on a-b.md and the later write silently overwrote the
	// earlier (#775's "distinct keys never map to the same file" promise).
	// Comparing against the folded result catches every mutation: charset
	// replacements, dash collapses, and edge trims all change the string.
	if safe == key {
		return safe
	}
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s-%x", safe, sum[:4])
}
