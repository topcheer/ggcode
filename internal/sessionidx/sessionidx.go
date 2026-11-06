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
	Trigrams    map[string][]Posting
	SourceBytes int64 // size of jsonl at build time
	BuiltAt     int64
}

// Index is the in-memory index for one session file. Safe for concurrent
// use; the source JSONL is append-only so incremental tailing is sound.
type Index struct {
	mu       sync.RWMutex
	Session  string
	SrcPath  string
	IdxPath  string
	data     indexFile
	lastMod  time.Time
	loadedAt time.Time
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

const idxVersion = 1

// Size returns the approximate memory footprint of loaded postings.
func (ix *Index) Size() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	n := 0
	for _, ps := range ix.data.Trigrams {
		n += len(ps) * 40
	}
	return n
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
		ix.data.Trigrams = map[string][]Posting{}
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
	}
	idx.data.Version = idxVersion
	idx.data.Trigrams = map[string][]Posting{}

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
		debug.Log("sessionidx", "load: idx for %s is %dMB, skipping decode (capped)", sessionID, fi.Size()>>20)
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
	idx.data = d
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
	if st.Size() < ix.data.IndexedTo {
		// Source truncated: full rebuild.
		ix.data = indexFile{Version: idxVersion, Trigrams: map[string][]Posting{}}
	}
	if st.Size() == ix.data.IndexedTo {
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
	if _, err := f.Seek(ix.data.IndexedTo, io.SeekStart); err != nil {
		return err
	}

	br := bufio.NewReader(f)
	offset := ix.data.IndexedTo
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
			ix.data.LineCount++
		}
		offset += consumed
		if rerr != nil {
			break // io.EOF (or error): stop at current progress
		}
	}
	ix.data.IndexedTo = offset
	ix.data.SourceBytes = st.Size()
	ix.data.BuiltAt = time.Now().UnixNano()
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
	if err := enc.Encode(&ix.data); err != nil {
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
	for _, txt := range rec.Texts {
		addPostings(ix.data.Trigrams, txt, offset, length, roleByte, rec.TsNano)
		total += len(txt)
	}
	ix.approxBytes += int64(total) * 40
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
// trigram occurrence (deduped per line) pointing at the whole line.
func addPostings(tg map[string][]Posting, text string, offset int64, length int32, role byte, ts int64) {
	if len(text) < 3 {
		return
	}
	seen := map[string]struct{}{}
	rs := make([]rune, 0, 64)
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
			tg[g] = append(tg[g], Posting{Offset: offset, Length: length, Role: role, TsNano: ts})
		}
	}
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
		p     Posting
		bits  uint8
		score int
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
			for _, p := range ix.data.Trigrams[g] {
				a := m[p.Offset]
				if a == nil {
					m[p.Offset] = &acc{p: p}
				} else {
					a.score++
				}
			}
		}
		// Keep only lines covering ALL trigrams of this term: approximate by
		// requiring score >= len(gr)-1 (each trigram adds one beyond the first).
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
