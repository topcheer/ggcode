package session

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// TransferBundleVersion is the schema version of exported session bundles.
// ImportBundleFile accepts bundles with Version <= TransferBundleVersion.
const TransferBundleVersion = 1

// ErrSessionNotFound is returned by ExportBundle when the session ID has no
// on-disk JSONL file in this store.
var ErrSessionNotFound = errors.New("session not found")

// TransferBundle is a portable, self-contained snapshot of one session: the
// raw JSONL records plus a copy of the index entry. It is what
// ExportBundle writes and ImportBundleFile consumes, enabling a session to
// be resumed on a different machine ("session handoff"): export here, copy
// the file anywhere, import there.
type TransferBundle struct {
	Version         int         `json:"version"`
	ExportedAt      time.Time   `json:"exported_at"`
	SessionID       string      `json:"session_id"`
	Title           string      `json:"title,omitempty"`
	SourceWorkspace string      `json:"source_workspace,omitempty"`
	MessageCount    int         `json:"message_count"`
	IndexEntry      *indexEntry `json:"index_entry,omitempty"`
	// Records are the raw JSONL lines of the session file (meta / message /
	// usage / cost / metric / checkpoint / tunnel records), in file order.
	Records []string `json:"records"`
}

// ExportBundle serializes the session identified by id into a gzip-compressed
// bundle at outPath (atomically: tmp file + rename). The bundle preserves the
// full message history — including pre-compaction records — and session
// metadata (title, pinned, tags, model info) so the importing side can resume
// the conversation exactly as it was. Returns the resolved output path.
func (s *JSONLStore) ExportBundle(id, outPath string) (string, error) {
	if !validSessionID(id) {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	if outPath == "" {
		outPath = id + ".ggsession"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrSessionNotFound, id)
		}
		return "", fmt.Errorf("reading session file: %w", err)
	}
	lines := splitNonEmptyLines(raw)
	if len(lines) == 0 {
		return "", fmt.Errorf("session %s has no records on disk", id)
	}

	bundle := TransferBundle{
		Version:      TransferBundleVersion,
		ExportedAt:   time.Now(),
		SessionID:    id,
		Records:      lines,
		MessageCount: countMessageRecords(lines),
	}

	// Best-effort index metadata (pinned/tags/msg-count are authoritative
	// in the index; the session file's meta records may lag). Absence is
	// not fatal — import falls back to the meta records.
	if idx, idxErr := s.loadIndexNoRepair(); idxErr == nil {
		for i := range idx {
			if idx[i].ID == id {
				e := idx[i]
				bundle.IndexEntry = &e
				bundle.Title = e.Title
				bundle.SourceWorkspace = e.Workspace
				break
			}
		}
	}

	if err := writeGzipAtomic(outPath, &bundle); err != nil {
		return "", err
	}
	return outPath, nil
}

// ImportOptions controls how an imported bundle is installed into the store.
type ImportOptions struct {
	// Workspace overrides the session workspace after import. Empty means
	// "the current working directory" — the common cross-device case, where
	// the source absolute path does not exist on this machine.
	Workspace string
	// KeepWorkspace preserves the bundle's original workspace path instead
	// of remapping. Ignored when Workspace is set.
	KeepWorkspace bool
}

// ImportBundleFile installs a bundle produced by ExportBundle into this
// store so the session shows up in the resume picker / session list.
//
// If the session ID collides with an existing session (or the ID is
// missing/invalid), a fresh ID is minted — imports never overwrite existing
// sessions. Record session_id fields are rewritten to the target ID and the
// workspace is remapped per opts, so the imported session is usable in the
// local workspace immediately.
func (s *JSONLStore) ImportBundleFile(path string, opts ImportOptions) (*Session, error) {
	bundle, err := readBundle(path)
	if err != nil {
		return nil, err
	}
	if bundle.Version <= 0 || bundle.Version > TransferBundleVersion {
		return nil, fmt.Errorf("unsupported bundle version %d (max %d)", bundle.Version, TransferBundleVersion)
	}
	if len(bundle.Records) == 0 {
		return nil, errors.New("bundle contains no records")
	}

	targetWS := opts.Workspace
	if targetWS == "" && !opts.KeepWorkspace {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			return nil, fmt.Errorf("resolving workspace: %w", wdErr)
		}
		targetWS = wd
	} else if targetWS == "" {
		targetWS = bundle.SourceWorkspace
	}

	s.mu.Lock()
	id := bundle.SessionID
	if !validSessionID(id) {
		id = generateID()
	}
	if s.sessionExistsLocked(id) {
		// Never overwrite: mint a fresh ID for the incoming copy.
		id = generateID()
	}

	var buf bytes.Buffer
	for i, line := range bundle.Records {
		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			s.mu.Unlock()
			return nil, fmt.Errorf("bundle record %d: invalid JSON: %w", i+1, err)
		}
		if rec.Type == "" {
			s.mu.Unlock()
			return nil, fmt.Errorf("bundle record %d: missing record type", i+1)
		}
		rec.SessionID = id
		if rec.Type == "meta" {
			rec.Workspace = targetWS
		}
		encoded, err := json.Marshal(rec)
		if err != nil {
			s.mu.Unlock()
			return nil, fmt.Errorf("bundle record %d: re-encoding: %w", i+1, err)
		}
		buf.Write(encoded)
		buf.WriteByte('\n')
	}

	if err := writeFileAtomic(s.sessionPath(id), buf.Bytes()); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()

	// Rebuild the index entry. Prefer the exported copy (it carries the
	// authoritative pre-transfer MsgCount / pinned / tags); patch the
	// identity and workspace fields for this store.
	entry := indexEntry{ID: id}
	if bundle.IndexEntry != nil {
		entry = *bundle.IndexEntry
		entry.ID = id
		entry.Workspace = targetWS
	}
	if entry.MsgCount < bundle.MessageCount {
		entry.MsgCount = bundle.MessageCount
	}

	ses, err := s.Load(id)
	if err != nil {
		return nil, fmt.Errorf("reloading imported session: %w", err)
	}
	ses.Workspace = targetWS
	if ses.Title == "" {
		ses.Title = bundle.Title
	}
	// Merge meta-record state the exported index entry may have lagged on
	// (pinned/tags are persisted as meta records first).
	if !entry.Pinned {
		entry.Pinned = ses.Pinned
	}
	if len(entry.Tags) == 0 {
		entry.Tags = ses.Tags
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = ses.CreatedAt
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = ses.UpdatedAt
	}
	if err := s.importIndexEntry(entry); err != nil {
		return nil, fmt.Errorf("updating index: %w", err)
	}
	return ses, nil
}

// importIndexEntry upserts a pre-built index entry under the index flock.
// Mirrors updateIndex's locking/dirty-flag discipline but takes an explicit
// entry: the exported MsgCount is authoritative even when the local reload
// is time-windowed (windowed loads under-report len(Messages)).
func (s *JSONLStore) importIndexEntry(entry indexEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, lockErr := lockWithBackoff(func() (func(), error) { return lockIndexFile(s.indexPath()) }, "importIndexEntry", "index")
	if lockErr != nil {
		s.indexDirty = true
		return lockErr
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	idx, err := s.loadIndexNoRepair()
	if err != nil {
		s.indexDirty = true
		return err
	}
	if idx == nil && s.indexDirty {
		return nil
	}
	found := false
	for i := range idx {
		if idx[i].ID == entry.ID {
			idx[i] = entry
			found = true
			break
		}
	}
	if !found {
		idx = append(idx, entry)
	}
	if err := s.saveIndex(idx); err != nil {
		s.indexDirty = true
		return err
	}
	s.indexDirty = false
	return nil
}

// sessionExistsLocked reports whether id already has a file or an index
// entry. Caller must hold s.mu.
func (s *JSONLStore) sessionExistsLocked(id string) bool {
	if _, err := os.Stat(s.sessionPath(id)); err == nil {
		return true
	}
	if idx, err := s.loadIndexNoRepair(); err == nil {
		for i := range idx {
			if idx[i].ID == id {
				return true
			}
		}
	}
	return false
}

// readBundle loads a bundle file, auto-detecting gzip compression via magic
// bytes so plain-JSON bundles remain readable.
func readBundle(path string) (*TransferBundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading bundle: %w", err)
	}
	var r io.Reader = bytes.NewReader(data)
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, gzErr := gzip.NewReader(r)
		if gzErr != nil {
			return nil, fmt.Errorf("opening gzip bundle: %w", gzErr)
		}
		defer gz.Close()
		r = gz
	}
	var bundle TransferBundle
	if err := json.NewDecoder(r).Decode(&bundle); err != nil {
		return nil, fmt.Errorf("parsing bundle: %w", err)
	}
	return &bundle, nil
}

// writeGzipAtomic marshals bundle as JSON, gzip-compresses it, and writes
// atomically (tmp + rename) with 0600 perms to match session files.
func writeGzipAtomic(outPath string, bundle *TransferBundle) error {
	plain, err := json.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("encoding bundle: %w", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(plain); err != nil {
		return fmt.Errorf("compressing bundle: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("finishing gzip: %w", err)
	}
	return writeFileAtomic(outPath, buf.Bytes())
}

// writeFileAtomic writes data to path via tmp file + fsync + rename.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("installing file: %w", err)
	}
	return nil
}

func splitNonEmptyLines(raw []byte) []string {
	var out []string
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == '\n' {
			line := string(bytes.TrimSuffix(raw[start:i], []byte("\r")))
			if line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	return out
}

// countMessageRecords counts type=="message" records without full decoding.
func countMessageRecords(lines []string) int {
	n := 0
	for _, line := range lines {
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &probe) == nil && probe.Type == "message" {
			n++
		}
	}
	return n
}
