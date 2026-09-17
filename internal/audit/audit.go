// Package audit implements a tamper-evident, hash-chained audit ledger of
// agent actions.
//
// The ledger is an append-only JSONL file where every entry commits to the
// previous one with a SHA-256 hash chain (entry.Hash covers all of the
// entry's own fields including entry.PrevHash, which holds the previous
// entry's Hash; the genesis entry links to a fixed all-zeros prev hash).
// This is the "cryptographic governance audit trail" pattern from 2025-2026
// agent-governance work (EU AI Act Article 12 logging obligations, OWASP
// LLM06, SIEM-forward agent auditing): standard logs can be rewritten after
// the fact, a hash chain cannot — any edit, reorder, or un-anchored tail
// deletion breaks verification at a precisely reportable position.
//
// Threat model (tamper-EVIDENT, not tamper-PROOF): the ledger detects
//   - editing any entry in place        → hash mismatch at that seq
//   - reordering or inserting entries   → sequence/prev-hash link mismatch
//   - deleting trailing entries         → head-anchor mismatch (Anchor)
//
// It does not defend against an adversary who recomputes the whole chain
// AND replaces the head anchor; that requires external anchoring (SIEM
// export, WORM storage) which is the operator's responsibility.
//
// Durability follows the same rule as the tool tape: flush after every
// append. Auditing a crash or a hang is exactly when the tail matters most,
// so a ledger captured up to the failure point is strictly more valuable
// than none.
package audit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Status values for audited actions.
const (
	StatusOK        = "ok"
	StatusError     = "error"
	StatusCancelled = "cancelled"
	StatusInvalid   = "invalid" // rejected before execution (e.g. preflight)
)

// GenesisPrevHash is the prev_hash of the first entry in a chain.
const GenesisPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"

// headVersion is the schema version of the .head anchor sidecar.
const headVersion = 1

// Event is a single audited action, submitted to Ledger.Append.
// InputHash is expected to be the canonical SHA-256 of the action's input
// JSON (see toolreplay.HashInput); raw inputs are deliberately NOT stored —
// tool arguments can carry secrets, and a governance ledger should prove
// what ran without duplicating sensitive payloads.
type Event struct {
	Tool       string // tool (or action) name
	Status     string // StatusOK / StatusError / StatusCancelled / StatusInvalid
	InputHash  string // canonical SHA-256 hex of input JSON
	DurationMS int64  // wall time of the action in milliseconds
	Err        string // short error/result summary, already truncated by the caller
	Session    string // optional per-event session override (falls back to the ledger's)
}

// Entry is a sealed ledger record: the event plus chain bookkeeping.
type Entry struct {
	Seq        int64  `json:"seq"`               // 1-based position in the chain
	Time       string `json:"time"`              // RFC3339Nano UTC
	Session    string `json:"session,omitempty"` // session ID, optional
	Tool       string `json:"tool"`
	Status     string `json:"status"`
	InputHash  string `json:"input_hash"`
	DurationMS int64  `json:"duration_ms"`
	Err        string `json:"err,omitempty"`
	PrevHash   string `json:"prev_hash"` // previous entry's Hash (genesis: zeros)
	Hash       string `json:"hash"`      // SHA-256 over all fields above
}

// hashEntry computes the chain hash over every field of e except Hash
// itself. Fields are length-prefixed so no separator ambiguity (an error
// message containing "|" or "\x00") can make two different entries collide.
func hashEntry(e Entry) string {
	var b strings.Builder
	writeField := func(s string) {
		var lenBuf [8]byte
		binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(s)))
		b.Write(lenBuf[:])
		b.WriteString(s)
	}
	writeField(fmt.Sprintf("%d", e.Seq))
	writeField(e.Time)
	writeField(e.Session)
	writeField(e.Tool)
	writeField(e.Status)
	writeField(e.InputHash)
	writeField(fmt.Sprintf("%d", e.DurationMS))
	writeField(e.Err)
	writeField(e.PrevHash)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Ledger is an append-only, crash-safe audit ledger backed by a JSONL file.
// All methods are safe for concurrent use.
type Ledger struct {
	mu      sync.Mutex
	path    string
	session string
	f       *os.File
	seq     int64  // last written seq
	prev    string // last written hash (or GenesisPrevHash)
}

// headFile is the .head anchor sidecar: a signed-off statement of where the
// chain ended as of Anchor time. Verify uses it to detect tail truncation.
type headFile struct {
	Version int    `json:"version"`
	Seq     int64  `json:"seq"`
	Hash    string `json:"hash"`
	Time    string `json:"time"`
}

// Open opens (creating if needed) the ledger at path and recovers the chain
// position from any existing contents, so re-opening the same file within a
// later session continues the same chain instead of forking a second one
// that would verify as broken.
func Open(path, session string) (*Ledger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open ledger: %w", err)
	}
	l := &Ledger{path: path, session: session, f: f, prev: GenesisPrevHash}
	entries, scanErr := scanEntries(path)
	if scanErr == nil && len(entries) > 0 {
		last := entries[len(entries)-1]
		l.seq = last.Seq
		l.prev = last.Hash
	}
	// A scan error on an existing file is deliberately tolerated here (the
	// file may be mid-verification by another process); Append will still
	// extend the file, and Verify will surface the pre-existing corruption.
	return l, nil
}

// Path returns the ledger file path.
func (l *Ledger) Path() string { return l.path }

// Append seals e into the chain, assigns seq/time/hash, persists the entry
// as one JSONL line, and returns the sealed entry. It never blocks on fsync
// (append + OS flush semantics match the tool tape's crash-safety posture);
// a write error is returned to the caller, which is expected to log and
// continue — an audit sink must never take the agent down.
func (l *Ledger) Append(e Event) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return Entry{}, fmt.Errorf("audit: ledger is closed")
	}
	session := l.session
	if e.Session != "" {
		session = e.Session
	}
	entry := Entry{
		Seq:        l.seq + 1,
		Time:       time.Now().UTC().Format(time.RFC3339Nano),
		Session:    session,
		Tool:       e.Tool,
		Status:     e.Status,
		InputHash:  e.InputHash,
		DurationMS: e.DurationMS,
		Err:        e.Err,
		PrevHash:   l.prev,
	}
	entry.Hash = hashEntry(entry)
	line, err := json.Marshal(entry)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: marshal entry: %w", err)
	}
	line = append(line, '\n')
	if _, err := l.f.Write(line); err != nil {
		return Entry{}, fmt.Errorf("audit: write entry: %w", err)
	}
	l.seq = entry.Seq
	l.prev = entry.Hash
	return entry, nil
}

// Anchor writes the <path>.head sidecar recording the current chain head.
// Calling it periodically (and at session end) upgrades tail truncation from
// undetectable to detected: Verify compares the file against the anchor.
func (l *Ledger) Anchor() error {
	l.mu.Lock()
	hf := headFile{
		Version: headVersion,
		Seq:     l.seq,
		Hash:    l.prev,
		Time:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	l.mu.Unlock()
	return writeHeadFile(l.path+".head", hf)
}

// Close flushes and closes the underlying file. Append after Close errors.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

func writeHeadFile(path string, hf headFile) error {
	data, err := json.Marshal(hf)
	if err != nil {
		return fmt.Errorf("audit: marshal head: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("audit: write head: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("audit: rename head: %w", err)
	}
	return nil
}

// Break describes the first verification failure found in a ledger file.
type Break struct {
	Seq    int64  // seq of the offending entry (0 for a malformed preamble line)
	Reason string // human-readable cause
}

// Truncation describes a head-anchor vs file mismatch: the anchor says the
// chain reached AnchoredSeq, but the file ends at FileSeq.
type Truncation struct {
	AnchoredSeq int64
	FileSeq     int64
}

// Report is the result of Verify.
type Report struct {
	Entries      int
	LastHash     string
	FirstBreak   *Break      // nil when the chain is intact
	HeadAnchored bool        // a .head sidecar was found
	Truncated    *Truncation // non-nil when the anchor proves missing tail entries
}

// OK reports whether verification found no problems.
func (r Report) OK() bool { return r.FirstBreak == nil && r.Truncated == nil }

// Verify recomputes the hash chain over the ledger file at path and checks
// it against the .head anchor when present. It stops at the first break —
// everything after an early break is untrusted anyway — and returns a
// structured report rather than an error, because a broken ledger is a
// successful verification outcome.
func Verify(path string) (Report, error) {
	entries, err := scanEntries(path)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Entries: len(entries)}
	prev := GenesisPrevHash
	expect := int64(1)
	for _, e := range entries {
		switch {
		case e.Seq != expect:
			rep.FirstBreak = &Break{Seq: e.Seq, Reason: fmt.Sprintf("sequence gap: expected seq %d", expect)}
		case e.PrevHash != prev:
			rep.FirstBreak = &Break{Seq: e.Seq, Reason: "prev_hash link mismatch (entry reordered, inserted, or predecessor edited)"}
		case e.Hash != hashEntry(e):
			rep.FirstBreak = &Break{Seq: e.Seq, Reason: "hash mismatch (entry content edited after sealing)"}
		default:
			prev = e.Hash
			expect++
			rep.LastHash = e.Hash
			continue
		}
		// rep.LastHash deliberately stays at the last verified entry.
		return rep.finalize(path, int64(len(entries))), nil
	}
	return rep.finalize(path, int64(len(entries))), nil
}

func (r Report) finalize(path string, lastSeq int64) Report {
	hf, err := readHeadFile(path + ".head")
	if err != nil {
		return r
	}
	r.HeadAnchored = true
	if hf.Version != headVersion {
		return r
	}
	if hf.Seq > lastSeq {
		r.Truncated = &Truncation{AnchoredSeq: hf.Seq, FileSeq: lastSeq}
	}
	return r
}

func readHeadFile(path string) (headFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return headFile{}, err
	}
	var hf headFile
	if err := json.Unmarshal(data, &hf); err != nil {
		return headFile{}, fmt.Errorf("audit: parse head: %w", err)
	}
	return hf, nil
}

// scanEntries parses the JSONL ledger, skipping blank lines. It returns a
// hard error only for an unreadable file; a malformed line inside the file
// is itself evidence of tampering (or corruption) and is surfaced as a
// sentinel entry with Seq 0, letting Verify report it as a break.
func scanEntries(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("audit: read ledger: %w", err)
	}
	var entries []Entry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			entries = append(entries, Entry{Seq: 0, Tool: "malformed line"})
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}
