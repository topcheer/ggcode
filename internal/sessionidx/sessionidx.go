// Package sessionidx provides a trigram inverted index over session JSONL
// files so agents can recall content from arbitrarily old sessions without
// re-reading (and re-parsing) the full history on every query.
//
// Ownership model: each ggcode instance only writes the index for its own
// live session. Indexes for sibling sessions in the same workspace are
// written by their owning instances; this instance loads them read-only and
// refreshes them periodically.
package sessionidx

import (
	"sort"

	"bufio"
	"bytes"

	"encoding/gob"
	"fmt"

	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Posting points at one matching line (record) in the source JSONL file.
type Posting struct {
	Offset int64 // byte offset of the line start
	Length int32 // line length in bytes (without newline)
	Role   byte  // 0=other, 1=user, 2=assistant, 3=tool
	TsNano int64 // record timestamp
}

// indexFile is the persisted representation of one session's index.
type indexFile struct {
	Version     int
	IndexedTo   int64 // byte offset covered in the source jsonl
	LineCount   int32
	Trigrams    map[string][]Posting // v1 format (nil in v2)
	SourceBytes int64                // size of jsonl at build time
	BuiltAt     int64
	// v2 grouped format (#5149 disk): postings grouped per source line so
	// line metadata (offset/length/role/ts) is stored ONCE per line instead
	// of once per posting, and trigrams become dictionary IDs. Measured on
	// the 937MB production session: v1 938MB -> v2 ~120MB (~8x).
	Dict  []string  // distinct trigrams, sorted; IDs are dict indexes
	Lines []lineRec // one record per indexed source line, sorted by Offset
}

// lineRec is the v2 grouped on-disk unit: one indexed line's metadata plus
// the sorted dictionary IDs of the trigrams it contains.
type lineRec struct {
	Offset int64
	Length int32
	Role   byte
	TsNano int64
	TriIDs []uint32
}

// Index is the in-memory index for one session file. Safe for concurrent
// use; the source JSONL is append-only so incremental tailing is sound.
//
// #5731 memory shape: postings are stored GROUPED, mirroring the v2 disk
// format - line metadata (offset/length/role/ts) lives ONCE in lines, and
// each posting is a 4-byte line id appended to the trigram's id list. The
// pre-#5731 shape (map[string][]Posting) duplicated the 24-byte line
// metadata per posting, which the heap profile flagged as a 212MB
// single-site (addPostings, 1337 live backing arrays averaging ~159KB)
// while the instance climbed 1675->2225MB inuse. 4B ids cut the resident
// posting heap ~5x measured end to end (24.67 -> 4.92 B/posting on a
// 284k-posting corpus; 6x per posting element) at identical recall and
// identical budget arithmetic (Size keeps the 40B/posting counting unit,
// so caps trip at the same posting counts as before).
type Index struct {
	mu      sync.RWMutex
	Session string
	SrcPath string
	IdxPath string
	// triID maps each distinct trigram to a dense id assigned in
	// insertion order; the sorted dictionary exists only transiently at
	// save time (groupedLocked).
	triID map[string]uint32
	// posts maps trigram id -> line ids holding that trigram, ascending by
	// offset (lines are appended in source order during Refresh, and Load
	// preserves the persisted Offset sort).
	posts map[uint32][]uint32
	// lines holds one record per INDEXED line (lines that produced at
	// least one posting), offset-ascending. TriIDs is nil in memory; the
	// line-major form is rebuilt transiently by groupedLocked at save.
	lines []lineRec
	// index build/tail state - the persisted fields of indexFile.
	indexedTo   int64 // byte offset covered in the source jsonl
	lineCount   int32 // all non-empty source lines seen (not just indexed)
	sourceBytes int64 // size of jsonl at build time
	builtAt     int64
	lastMod     time.Time
	loadedAt    time.Time
	// rootDir is the sessions directory this index was loaded from; paths
	// are rebuilt from it (never taken raw) when reading snippets.
	rootDir string
	// corrupt: the persisted gob failed to decode (真损坏 - degrade to
	// source scan). stale: the gob decoded but carries another
	// idxVersion (format upgrade - NOT corrupt, an owning instance
	// rebuilds it on its next Refresh).
	corrupt bool
	stale   bool
	// capped: the self-budget cap was hit - trigram ingestion stops while
	// IndexedTo keeps advancing (nothing re-decodes), and Query degrades
	// to a bounded source scan via the manager.
	capped bool
	// approxBytes tracks the estimated postings memory (the same 40B /
	// posting basis Size() uses) so the cap check is O(1) per line.
	approxBytes int64
	// selfBudget caps postings memory for THIS instance's live-session
	// index; 0 disables the cap (external entries stay governed by the
	// manager's machine budget).
	selfBudget int64
}

const idxVersion = 2

// Size returns the approximate memory footprint of loaded postings.
// The 40B/posting basis is the BUDGET's counting unit, deliberately kept
// stable across the #5731 grouped-shape change so cap behavior (posting
// counts at which ingestion stops) is unchanged; the real resident cost
// per posting is now ~4B.
func (ix *Index) Size() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	n := 0
	for _, ls := range ix.posts {
		n += len(ls) * 40
	}
	return n
}

// resetLocked clears all in-memory index state. Caller holds ix.mu.
func (ix *Index) resetLocked() {
	ix.triID = map[string]uint32{}
	ix.posts = map[uint32][]uint32{}
	ix.lines = nil
	ix.indexedTo = 0
	ix.lineCount = 0
	ix.sourceBytes = 0
	ix.builtAt = 0
}

// SetSelfBudget caps this index's postings memory (bytes, approximate).
// Only meaningful for the live self index; 0 disables the cap.
func (ix *Index) SetSelfBudget(b int64) {
	ix.mu.Lock()
	ix.selfBudget = b
	if b > 0 && ix.approxBytes > b {
		// Retroactive: already over cap (Start loaded a huge persisted
		// idx) - drop the postings NOW; recall degrades to the bounded
		// source scan. approxBytes stays above the cap so ingestion
		// never resumes; IndexedTo is untouched so nothing re-decodes.
		// #5731: the whole grouped store goes - triID, posts and lines
		// are one unit (line ids are meaningless without lines).
		ix.triID = map[string]uint32{}
		ix.posts = map[uint32][]uint32{}
		ix.lines = nil
		ix.capped = true
	}
	ix.mu.Unlock()
}

// Capped reports whether ingestion stopped at the self-budget cap.
func (ix *Index) Capped() bool {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.capped
}

// approxBytesPerSourceByte is the postings-memory estimate charged per
// indexed text byte (indexLine). It also drives the tail-build skip gate:
// a source whose RAW size already implies an over-budget index can never
// persist (capped builds are not saved), so building it would only churn.
const approxBytesPerSourceByte = 40

// DefaultSelfBudget is the resident cap for one instance's live-session
// index (env GGCODE_SESSIONIDX_SELF_BUDGET, bytes). A live session whose
// transcript outgrows the cap degrades its recall queries to a bounded
// source scan instead of holding gigabytes in the heap - the #5149
// amplification class, self-side this time (a 547MB idx file decoded to
// ~2-3GB of Go heap on 2026-11-06).
func DefaultSelfBudget() int64 {
	if v := os.Getenv("GGCODE_SESSIONIDX_SELF_BUDGET"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 256 << 20
}

// MaxIndexableSourceBytes returns the raw source size above which a fresh
// build is guaranteed to end capped (budget / approxBytesPerSourceByte).
// budget <= 0 (cap disabled) disables the gate too.
func MaxIndexableSourceBytes(budget int64) int64 {
	if budget <= 0 {
		return 0
	}
	return budget / approxBytesPerSourceByte
}

// idxSkipDecodeBytes is the persisted-idx size above which Load skips the
// gob decode outright (test-overridable). At the conventional 3-5x postings
// expansion, a 64MB idx file implies well over the 256MB self budget.
var idxSkipDecodeBytes int64 = 64 << 20

// IdxDir returns the directory holding index files for a sessions dir.
func IdxDir(sessionsDir string) string {
	return filepath.Join(sessionsDir, "idx")
}

// IdxPath returns the index file path for a session id.
func IdxPath(sessionsDir, sessionID string) string {
	return filepath.Join(IdxDir(sessionsDir), sessionID+".idx")
}

// Load reads an existing .idx for the session, or returns an empty index
// when none exists. It does NOT tail the source; call Refresh for that.
func Load(sessionsDir, sessionID string) (*Index, error) {
	// CodeQL path-injection guard: the id is joined into filesystem
	// paths, and it can arrive from tool arguments.
	if !ValidSessionID(sessionID) {
		return nil, fmt.Errorf("sessionidx: invalid session id")
	}
	src := filepath.Join(sessionsDir, sessionID+".jsonl")
	idx := &Index{
		Session: sessionID,
		SrcPath: src,
		IdxPath: IdxPath(sessionsDir, sessionID),
		rootDir: sessionsDir,
		triID:   map[string]uint32{},
		posts:   map[uint32][]uint32{},
	}

	// #5149 startup-spike guard: decoding a multi-hundred-MB gob spikes the
	// heap to gigabytes before SetSelfBudget can drop it (RSS then sits at
	// the GC/MADV_FREE lag peak - observed 5.7GB at 3.5min uptime). When the
	// persisted idx is already past the self-budget scale, skip the decode
	// entirely: capped from birth, recall degrades to the bounded source
	// scan, and the owning instance never rebuilds (capped Refresh is a
	// no-op), so nothing re-materializes the map.
	f, err := os.Open(idx.IdxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return idx, nil
		}
		return nil, err
	}
	defer f.Close()
	// #5149 startup-spike guard: decoding a multi-hundred-MB gob spikes the
	// heap to gigabytes before SetSelfBudget can drop it (RSS then sits at
	// the GC/MADV_FREE lag peak - observed 5.7GB at 3.5min uptime). Size is
	// taken from the ALREADY-OPEN handle (f.Stat, no new path-dependent
	// operation); oversized files skip the decode entirely - capped from
	// birth, recall degrades to the bounded source scan, and nothing ever
	// re-materializes the giant map.
	if fi, err := f.Stat(); err == nil && fi.Size() > idxSkipDecodeBytes {
		// #5149 dead-idx cleanup: past the skip threshold the file is
		// PERMANENTLY useless - every loader gets capped, capped Refresh is
		// a no-op, so no instance ever reads or rewrites it again. Delete
		// (derived data; the source jsonl remains, recall serves from the
		// bounded source scan) instead of leaving frozen gigabytes on disk.
		debug.Log("sessionidx", "load: idx for %s is %dMB over skip threshold - flagged dead (manager cleans it up)", sessionID, fi.Size()>>20)
		idx.capped = true
		idx.approxBytes = fi.Size() * 4 // postings-memory estimate, over any budget
		return idx, nil
	}
	dec := gob.NewDecoder(bufio.NewReader(f))
	var d indexFile
	if err := dec.Decode(&d); err != nil {
		// Corrupt index: start over rather than fail.
		debug.Log("sessionidx", "load: corrupt idx for %s (%v), rebuilding on refresh", sessionID, err)
		idx.corrupt = true
		return idx, nil
	}
	if d.Version != idxVersion {
		// Format upgrade: the owning instance rebuilds on its next
		// Refresh - readers just see this session as not-yet-reindexed.
		idx.stale = true
		return idx, nil
	}
	if d.Trigrams == nil {
		// v2 grouped: adopt the line records as the resident lines and
		// rebuild the inverted trigram-id -> line-ids map (#5731: one 4B
		// line id per posting instead of a 24B Posting duplicate of the
		// line metadata). Each line's TriIDs are consumed here and the
		// decode buffers released - keeping TriIDs resident would retain
		// a second copy of the posting relation.
		idx.triID = make(map[string]uint32, len(d.Dict))
		for id, tg := range d.Dict {
			idx.triID[tg] = uint32(id)
		}
		idx.posts = make(map[uint32][]uint32, len(d.Dict))
		idx.lines = make([]lineRec, len(d.Lines))
		for i, lr := range d.Lines {
			idx.lines[i] = lineRec{Offset: lr.Offset, Length: lr.Length, Role: lr.Role, TsNano: lr.TsNano}
			for _, id := range lr.TriIDs {
				if int(id) < len(d.Dict) {
					idx.posts[id] = append(idx.posts[id], uint32(i))
				}
			}
		}
		d.Dict = nil
		d.Lines = nil
	} else {
		// Legacy payload carrying the v1 map (a Version==2 file written
		// before the grouped in-memory shape): convert ONCE into the
		// grouped resident form. Postings per trigram arrive in source
		// (offset-ascending) order, so line ids are assigned by sorted
		// offset and each trigram's list stays ascending.
		meta := make(map[int64]lineRec)
		for _, ps := range d.Trigrams {
			for _, p := range ps {
				meta[p.Offset] = lineRec{Offset: p.Offset, Length: p.Length, Role: p.Role, TsNano: p.TsNano}
			}
		}
		offs := make([]int64, 0, len(meta))
		for off := range meta {
			offs = append(offs, off)
		}
		sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
		idx.lines = make([]lineRec, 0, len(offs))
		lidOf := make(map[int64]uint32, len(offs))
		for i, off := range offs {
			idx.lines = append(idx.lines, meta[off])
			lidOf[off] = uint32(i)
		}
		for tg, ps := range d.Trigrams {
			id, ok := idx.triID[tg]
			if !ok {
				id = uint32(len(idx.triID))
				idx.triID[tg] = id
			}
			for _, p := range ps {
				if lid, ok := lidOf[p.Offset]; ok {
					idx.posts[id] = append(idx.posts[id], lid)
				}
			}
		}
		d.Trigrams = nil
	}
	idx.indexedTo = d.IndexedTo
	idx.lineCount = d.LineCount
	idx.sourceBytes = d.SourceBytes
	idx.builtAt = d.BuiltAt
	idx.approxBytes = int64(idx.Size())
	idx.loadedAt = time.Now()
	return idx, nil
}

// Refresh incrementally tails the source JSONL from IndexedTo and merges new
// lines into the index. Only lines that produce postings are recorded. When
// the source shrank (should not happen for append-only files) the index is
// rebuilt from scratch.
func (ix *Index) Refresh() error {
	if ix.Capped() {
		// Capped (self-budget or skip-decode): no ingestion, no reads - the
		// bounded source scan serves queries; keep it that way.
		return nil
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()

	st, err := os.Stat(ix.SrcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.Size() < ix.indexedTo {
		// Source truncated: full rebuild.
		ix.resetLocked()
	}
	if st.Size() == ix.indexedTo {
		if ix.selfBudget > 0 && ix.approxBytes > ix.selfBudget {
			ix.capped = true // already-over-cap: flag even with no new tail
		}
		return nil
	}

	f, err := os.Open(ix.SrcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(ix.indexedTo, io.SeekStart); err != nil {
		return err
	}

	br := bufio.NewReader(f)
	offset := ix.indexedTo
	for {
		line, rerr := br.ReadBytes('\n')
		consumed := int64(len(line))
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(trimmed) > 0 {
			if ix.selfBudget > 0 && ix.approxBytes >= ix.selfBudget {
				// Self-budget cap hit: stop ingesting postings but keep
				// advancing IndexedTo so the tail is never re-decoded.
				ix.capped = true
			} else {
				ix.indexLine(offset, int32(len(trimmed)), trimmed)
			}
			ix.lineCount++
		}
		offset += consumed
		if rerr != nil {
			break // io.EOF (or error): stop at current progress
		}
	}
	ix.indexedTo = offset
	ix.sourceBytes = st.Size()
	ix.builtAt = time.Now().UnixNano()
	// #5710: the per-line gate above only runs BEFORE each line, so a
	// refresh whose LAST line (or only line) pushes approxBytes over the
	// budget ends over-cap yet uncapped - the flag then waits for the
	// next refresh tick (30min in production), during which the index
	// keeps serving and holding heap far past the budget. Re-check after
	// the loop so the cap is final within the same refresh.
	if ix.selfBudget > 0 && ix.approxBytes >= ix.selfBudget {
		ix.capped = true
	}
	if ix.capped {
		// Capped: do NOT persist. The in-RAM state is emptied postings plus
		// an advanced IndexedTo; saving it would reload as capped=false with
		// an empty index and no source-scan fallback (silent recall loss).
		// The last-good idx stays on disk; on reload SetSelfBudget's
		// retroactive drop re-caps it and the degraded scan re-engages.
		//
		// #5710: drop the partial postings NOW too. A capped index serves
		// no queries (Query returns nil; recall degrades to the bounded
		// source scan), so the store accumulated before the cap tripped
		// is dead weight that can approach the whole budget in heap -
		// the SetLiveID path (fresh sessions) hit exactly this on its
		// way to the 1-3.3GB heapInuse class. approxBytes stays above
		// the cap so ingestion never resumes and nothing re-decodes.
		// #5731: the grouped store (triID+posts+lines) goes as one unit.
		ix.triID = map[string]uint32{}
		ix.posts = map[uint32][]uint32{}
		ix.lines = nil
		return nil
	}
	return ix.saveLocked()
}

// saveLocked persists the index atomically (tmp+rename). Caller holds ix.mu.
func (ix *Index) saveLocked() error {
	dir := filepath.Dir(ix.IdxPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-idx-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	bw := bufio.NewWriter(tmp)
	enc := gob.NewEncoder(bw)
	gl := ix.groupedLocked()
	if err := enc.Encode(gl); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := bw.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, ix.IdxPath)
}

// indexLine parses one JSONL line (bounded scan) and adds postings.
func (ix *Index) indexLine(offset int64, length int32, line []byte) {
	// Lightweight record classification without full json.Unmarshal of the
	// whole record: find "type":"message" and the role + timestamp fields,
	// then extract text blocks. Full unmarshal of huge tool results would
	// double memory; a scanner approach keeps the hot path cheap. We still
	// need structured text extraction, so unmarshal only when the line is
	// plausibly a message record (size gate to bound worst-case cost).
	const maxIndexable = 8 << 20 // 8MB per line
	if length > maxIndexable {
		return
	}
	if !bytes.Contains(line, []byte(`"type":"message"`)) {
		return
	}
	rec, ok := decodeMessageRecord(line)
	if !ok {
		return
	}
	roleByte := roleToByte(rec.Role)
	total := 0
	// Line-level dedup across all text blocks of the record (#5201): the
	// same trigram appearing in two blocks of one line used to add two
	// postings with the same offset, and Query's coverage arithmetic
	// (score >= len(gr)-1) assumes each distinct trigram contributes at
	// most one posting per line. One shared seen map keeps that contract.
	//
	// #5731: the line record is materialized lazily (on the FIRST posting)
	// so lines whose blocks never yield a trigram still cost nothing, and
	// every posting is the line's 4B id rather than a 24B Posting copy.
	lr := lineRec{Offset: offset, Length: length, Role: roleByte, TsNano: rec.TsNano}
	lid := int32(-1)
	seen := map[string]struct{}{}
	for _, txt := range rec.Texts {
		lid = ix.addPostings(seen, txt, lr, lid)
		total += len(txt)
	}
	ix.approxBytes += int64(total) * approxBytesPerSourceByte
}

func roleToByte(role string) byte {
	switch role {
	case "user":
		return 1
	case "assistant":
		return 2
	default:
		if strings.Contains(role, "tool") {
			return 3
		}
		return 0
	}
}

// addPostings tokenizes text into rune trigrams and records one posting per
// distinct trigram (deduped per line via the caller-owned seen map shared
// across all text blocks of the record, #5201) pointing at the whole line.
//
// #5731: a posting is the line's id in ix.lines, not a Posting struct - the
// line's Offset/Length/Role/TsNano live once in the line record, so each
// posting costs 4 bytes of backing array instead of 24 plus append slack.
// This is the site the #5731 heap profile flagged (212MB inuse, 1337 live
// backing arrays ~159KB each) when it appended full Posting values.
// lr/lid carry the pending line: the line record is appended on the first
// posting of the line and the assigned id is returned so later blocks of
// the same line reuse it (lid < 0 = not yet materialized).
func (ix *Index) addPostings(seen map[string]struct{}, text string, lr lineRec, lid int32) int32 {
	if len(text) < 3 {
		return lid
	}
	rs := make([]rune, 0, 4)
	for _, r := range text {
		rs = append(rs, r)
		if len(rs) > 3 {
			rs = rs[1:]
		}
		if len(rs) == 3 {
			g := string(rs)
			if _, dup := seen[g]; dup {
				continue
			}
			seen[g] = struct{}{}
			if lid < 0 {
				lid = int32(len(ix.lines))
				ix.lines = append(ix.lines, lr)
			}
			id, ok := ix.triID[g]
			if !ok {
				id = uint32(len(ix.triID))
				ix.triID[g] = id
			}
			ix.posts[id] = append(ix.posts[id], uint32(lid))
		}
	}
	return lid
}

// Query searches the index. Terms are ANDed; each term is matched if all of
// its trigrams are present (with per-trigram hit lines intersected). Short
// terms (<3 runes) fall back to substring scan over candidate lines of the
// source file that any index trigram of the term's prefix touches — for
// simplicity, short terms are matched by scanning the trigram union of the
// term's runes.
type Hit struct {
	Offset   int64
	Length   int32
	Role     byte
	TsNano   int64
	TermBits uint8 // bitmask of which query terms matched this line
}

func (ix *Index) Query(terms []string, limit int) []Hit {
	if ix.capped {
		return nil // capped: the manager degrades to a bounded source scan
	}
	limit = clampLimit(limit)
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if len(terms) == 0 || limit <= 0 {
		return nil
	}
	// lineSet: offset → hit accumulator
	type acc struct {
		p     lineRec // #5731: metadata comes from the shared line record
		bits  uint8
		score int
		got   map[string]struct{} // distinct trigrams of this term seen for the line
	}
	sets := make([]map[int64]*acc, len(terms))
	for ti, term := range terms {
		gr := trigrams(term)
		if len(gr) == 0 {
			// term shorter than 3 runes: use its runes' trigram neighborhood
			gr = trigrams(term + "  ")
		}
		m := map[int64]*acc{}
		for _, g := range gr {
			id, ok := ix.triID[g]
			if !ok {
				continue // trigram never indexed: contributes no postings
			}
			for _, lid := range ix.posts[id] {
				p := ix.lines[lid]
				a := m[p.Offset]
				if a == nil {
					a = &acc{p: p, got: map[string]struct{}{g: {}}}
					m[p.Offset] = a
				} else {
					// #5201: legacy on-disk indexes can carry duplicate
					// postings for the same (line, trigram) when the same
					// trigram appeared in multiple text blocks. Score must
					// count DISTINCT trigrams of the term that hit the
					// line, otherwise a line missing a required trigram
					// can still pass the coverage threshold.
					if _, dup := a.got[g]; !dup {
						a.got[g] = struct{}{}
						a.score++
					}
				}
			}
		}
		// Keep only lines covering ALL trigrams of this term: score counts
		// distinct term trigrams hitting the line (see #5201 note above).
		keep := map[int64]*acc{}
		for off, a := range m {
			if a.score >= len(gr)-1 {
				keep[off] = a
			}
		}
		if len(keep) == 0 {
			return nil // AND semantics: one term with no lines kills the query
		}
		sets[ti] = keep
	}
	// Intersect: smallest set first.
	order := make([]int, len(sets))
	for i := range order {
		order[i] = i
	}
	// simple selection sort by size (few terms)
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if len(sets[order[j]]) < len(sets[order[i]]) {
				order[i], order[j] = order[j], order[i]
			}
		}
	}
	base := sets[order[0]]
	out := make([]Hit, 0, limit)
	for off, a := range base {
		ok := true
		for _, ti := range order[1:] {
			if _, hit := sets[ti][off]; !hit {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		bits := uint8(0)
		for ti := range sets {
			if _, hit := sets[ti][off]; hit {
				bits |= 1 << ti
			}
		}
		out = append(out, Hit{Offset: a.p.Offset, Length: a.p.Length, Role: a.p.Role, TsNano: a.p.TsNano, TermBits: bits})
		if len(out) >= limit*3 { // gather headroom for role filtering upstream
			break
		}
	}
	// newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// trigrams returns the distinct rune trigrams of s (lower-cased).
func trigrams(s string) []string {
	var out []string
	seen := map[string]struct{}{}
	rs := make([]rune, 0, 8)
	for _, r := range strings.ToLower(s) {
		rs = append(rs, r)
		if len(rs) > 3 {
			rs = rs[1:]
		}
		if len(rs) == 3 {
			g := string(rs)
			if _, d := seen[g]; !d {
				seen[g] = struct{}{}
				out = append(out, g)
			}
		}
	}
	return out
}

// ReadLineAt reads the line at offset with the given length from the source
// file (used to materialize snippets at query time).
func (ix *Index) ReadLineAt(offset int64, length int32) ([]byte, error) {
	ix.mu.RLock()
	dir, sess := ix.rootDir, ix.Session
	ix.mu.RUnlock()
	// Rebuild the path from validated parts instead of trusting a stored
	// path: keeps this read inside the sessions directory (CodeQL
	// path-injection finding on the #5164 diff).
	if !ValidSessionID(sess) {
		return nil, fmt.Errorf("sessionidx: invalid session id")
	}
	f, err := os.Open(filepath.Join(dir, sess+".jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return nil, err
	}
	return buf, nil
}

// decodeMessageRecord extracts role, timestamp, and text-block contents from
// a "message" JSONL line using a bounded scan (json.Decoder over a
// bytes.Reader with UseNumber to avoid float allocation).
func decodeMessageRecord(line []byte) (msgRec, bool) {
	// Structural note: message records are {"type":"message",...,"message":{...}}
	// with content blocks [{"type":"text","text":"..."}|{"type":"tool_result","content":[...]}].
	var top struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Message   *struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	dec := jsonDecoder(line)
	if err := dec.Decode(&top); err != nil {
		return msgRec{}, false
	}
	if top.Type != "message" || top.Message == nil {
		return msgRec{}, false
	}
	r := msgRec{Role: top.Message.Role, TsNano: top.Timestamp.UnixNano()}
	for _, b := range top.Message.Content {
		if b.Type == "text" && b.Text != "" {
			r.Texts = append(r.Texts, b.Text)
		}
	}
	if len(r.Texts) == 0 {
		return msgRec{}, false
	}
	return r, true
}

type msgRec struct {
	Role   string
	Texts  []string
	TsNano int64
}

// groupedLocked renders the in-memory grouped store as the v2 grouped
// on-disk form (line records + trigram dictionary). Caller holds ix.mu.
func (ix *Index) groupedLocked() *indexFile {
	dict := make([]string, 0, len(ix.triID))
	for tg := range ix.triID {
		dict = append(dict, tg)
	}
	sort.Strings(dict)
	idOf := make(map[string]uint32, len(dict))
	for i, tg := range dict {
		idOf[tg] = uint32(i)
	}
	// In-memory trigram ids are insertion-ordered; remap them onto the
	// sorted dictionary. The line-major TriIDs exist ONLY here, transiently
	// at save time - keeping them resident would hold a second copy of the
	// posting relation (the exact per-posting amplification #5731 removed).
	strOf := make([]string, len(ix.triID))
	for tg, id := range ix.triID {
		strOf[id] = tg
	}
	lines := make([]lineRec, len(ix.lines))
	copy(lines, ix.lines)
	for oid, lids := range ix.posts {
		nid := idOf[strOf[oid]]
		for _, lid := range lids {
			lines[lid].TriIDs = append(lines[lid].TriIDs, nid)
		}
	}
	for i := range lines {
		lr := &lines[i]
		sort.Slice(lr.TriIDs, func(a, b int) bool { return lr.TriIDs[a] < lr.TriIDs[b] })
	}
	// lines is offset-ascending by construction; the sort is a stabilizer
	// against future construction paths, not a correctness dependency.
	sort.Slice(lines, func(i, j int) bool { return lines[i].Offset < lines[j].Offset })
	return &indexFile{
		Version:     idxVersion,
		IndexedTo:   ix.indexedTo,
		LineCount:   ix.lineCount,
		SourceBytes: ix.sourceBytes,
		BuiltAt:     ix.builtAt,
		Dict:        dict,
		Lines:       lines,
	}
}
