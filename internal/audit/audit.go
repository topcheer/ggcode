package audit

import (
	"bytes"
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

const (
	StatusOK           = "ok"
	StatusError        = "error"
	StatusCancelled    = "cancelled"
	StatusInvalid      = "invalid"
	StatusUserApproved = "approved"
	StatusUserDenied   = "user_denied"
	StatusAskTimeout   = "ask_timeout"
	StatusPolicyDenied = "policy_denied"
)

const GenesisPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"

const headVersion = 1

type Event struct {
	Tool        string
	Status      string
	InputHash   string
	DurationMS  int64
	Err         string
	Session     string
	InvariantID string
	Peer        string
	TaskID      string
}

type Entry struct {
	Seq         int64  `json:"seq"`
	Time        string `json:"time"`
	Session     string `json:"session,omitempty"`
	Tool        string `json:"tool"`
	Status      string `json:"status"`
	InputHash   string `json:"input_hash"`
	DurationMS  int64  `json:"duration_ms"`
	Err         string `json:"err,omitempty"`
	InvariantID string `json:"invariant_id,omitempty"`
	Peer        string `json:"peer,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	PrevHash    string `json:"prev_hash"`
	Hash        string `json:"hash"`
}

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
	if e.InvariantID != "" || e.Peer != "" || e.TaskID != "" {
		writeTagged := func(tag byte, s string) {
			b.WriteByte(tag)
			writeField(s)
		}
		writeTagged(1, e.InvariantID)
		writeTagged(2, e.Peer)
		writeTagged(3, e.TaskID)
	}
	writeField(e.PrevHash)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

type Ledger struct {
	mu      sync.Mutex
	path    string
	session string
	f       *os.File
	seq     int64
	prev    string
}

type headFile struct {
	Version int    `json:"version"`
	Seq     int64  `json:"seq"`
	Hash    string `json:"hash"`
	Time    string `json:"time"`
}

func Open(path, session string) (*Ledger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open ledger: %w", err)
	}
	l := &Ledger{path: path, session: session, f: f, prev: GenesisPrevHash}
	entries, scanErr := scanEntries(path)
	if scanErr == nil && len(entries) > 0 && isMalformedSentinel(entries[len(entries)-1]) {
		_ = truncateTornTail(f)
		entries, scanErr = scanEntries(path)
	}
	if scanErr == nil && len(entries) > 0 {
		last := entries[len(entries)-1]
		if !isMalformedSentinel(last) {
			l.seq = last.Seq
			l.prev = last.Hash
		}
	}
	return l, nil
}

func isMalformedSentinel(e Entry) bool {
	return e.Seq == 0 && e.Tool == "malformed line"
}

func truncateTornTail(f *os.File) error {
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return err
	}
	cut := len(data)
	for cut > 0 {
		end := cut
		if data[end-1] == '\n' {
			end--
		}
		if end == 0 {
			break
		}
		lineStart := bytes.LastIndexByte(data[:end], '\n') + 1
		line := bytes.TrimSpace(data[lineStart:end])
		if len(line) == 0 {
			break
		}
		var probe Entry
		if json.Unmarshal(line, &probe) == nil {
			break
		}
		cut = lineStart
	}
	if cut == len(data) {
		return nil
	}
	if err := f.Truncate(int64(cut)); err != nil {
		return err
	}
	return f.Sync()
}

func (l *Ledger) Path() string { return l.path }

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
		Seq:         l.seq + 1,
		Time:        time.Now().UTC().Format(time.RFC3339Nano),
		Session:     session,
		Tool:        e.Tool,
		Status:      e.Status,
		InputHash:   e.InputHash,
		DurationMS:  e.DurationMS,
		Err:         e.Err,
		InvariantID: e.InvariantID,
		Peer:        e.Peer,
		TaskID:      e.TaskID,
		PrevHash:    l.prev,
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

type Break struct {
	Seq    int64
	Reason string
}

type Truncation struct {
	AnchoredSeq int64
	FileSeq     int64
}

type Report struct {
	Entries      int
	LastHash     string
	FirstBreak   *Break
	HeadAnchored bool
	Truncated    *Truncation
}

func (r Report) OK() bool { return r.FirstBreak == nil && r.Truncated == nil }

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
