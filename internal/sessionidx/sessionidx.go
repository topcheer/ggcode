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

	"io"
	"os"
	"path/filepath"
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
	// corrupt: the persisted gob failed to decode (真损坏 - degrade to
	// source scan). stale: the gob decoded but carries another
	// idxVersion (format upgrade - NOT corrupt, an owning instance
	// rebuilds it on its next Refresh).
	corrupt bool
	stale   bool
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
	src := filepath.Join(sessionsDir, sessionID+".jsonl")
	idx := &Index{
		Session: sessionID,
		SrcPath: src,
		IdxPath: IdxPath(sessionsDir, sessionID),
	}
	idx.data.Version = idxVersion
	idx.data.Trigrams = map[string][]Posting{}

	f, err := os.Open(idx.IdxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return idx, nil
		}
		return nil, err
	}
	defer f.Close()
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
	idx.loadedAt = time.Now()
	return idx, nil
}

// Refresh incrementally tails the source JSONL from IndexedTo and merges new
// lines into the index. Only lines that produce postings are recorded. When
// the source shrank (should not happen for append-only files) the index is
// rebuilt from scratch.
func (ix *Index) Refresh() error {
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
			ix.indexLine(offset, int32(len(trimmed)), trimmed)
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
	for _, txt := range rec.Texts {
		addPostings(ix.data.Trigrams, txt, offset, length, roleByte, rec.TsNano)
	}
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
	src := ix.SrcPath
	ix.mu.RUnlock()
	f, err := os.Open(src)
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
