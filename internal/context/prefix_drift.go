// prefix_drift.go: LLM prefix drift pinpointing (#5807 follow-up).
//
// OFF by default. Enabled only when the anchor file ~/.ggcode/prefixdrifting
// exists (checked per-write with a short stat cache so hot paths stay cheap).
// On a system-head rewrite/insert, records a detailed fingerprint of BOTH
// old and new heads into ~/.ggcode/prefix-drift/drift-<pid>-<unixts>.jsonl:
//
//   - per-content-block: type, cache flag, byte length, sha256:16,
//     first-line digest (160 chars, whitespace-squashed), line map
//     (hash over every 64th line) to bracket drift inside a big block
//   - first-divergence line number between old/new full text with a
//     capped unified-diff-style context window
//
// One JSON line per record; append-only, rotated at 32MB.

package context

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	prefixDriftAnchorFile = "prefixdrifting"
	prefixDriftDir        = "prefix-drift"
	prefixDriftMaxBytes   = 32 << 20
	prefixDriftKeepFiles  = 4
)

var (
	prefixDriftSeq       atomic.Int64
	prefixDriftMu        sync.Mutex
	prefixDriftFile      *os.File
	prefixDriftPath      string
	prefixDriftEnabled   bool
	prefixDriftLastCheck time.Time
)

// prefixDriftOn reports whether the anchor file exists (2s stat cache).
func prefixDriftOn() bool {
	prefixDriftMu.Lock()
	defer prefixDriftMu.Unlock()
	if time.Since(prefixDriftLastCheck) < 2*time.Second {
		return prefixDriftEnabled
	}
	prefixDriftLastCheck = time.Now()
	home, err := os.UserHomeDir()
	if err != nil {
		prefixDriftEnabled = false
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".ggcode", prefixDriftAnchorFile))
	prefixDriftEnabled = err == nil
	return prefixDriftEnabled
}

type prefixDriftBlock struct {
	Index   int    `json:"i"`
	Type    string `json:"type"`
	Cache   bool   `json:"cache,omitempty"`
	Bytes   int    `json:"bytes"`
	Sha16   string `json:"sha16"`
	FirstLn string `json:"first160,omitempty"`
	LineMap string `json:"lineMap,omitempty"`
	Lines   int    `json:"lines"`
}

type prefixDriftRecord struct {
	Seq           int                `json:"seq"`
	Time          string             `json:"ts"`
	Event         string             `json:"event"`
	OldVersion    int64              `json:"oldVersion,omitempty"`
	NewVersion    int64              `json:"newVersion,omitempty"`
	NonTailSeq    int64              `json:"nonTailSeq,omitempty"`
	OldTokens     int                `json:"oldTokens"`
	NewTokens     int                `json:"newTokens"`
	OldBlocks     []prefixDriftBlock `json:"oldBlocks,omitempty"`
	NewBlocks     []prefixDriftBlock `json:"newBlocks,omitempty"`
	FirstDiffLine int                `json:"firstDiffLine"`
	DiffOld       string             `json:"diffOld,omitempty"`
	DiffNew       string             `json:"diffNew,omitempty"`
}

func blockFingerprint(idx int, blk provider.ContentBlock) prefixDriftBlock {
	fb := prefixDriftBlock{Index: idx, Type: blk.Type, Cache: blk.Cache, Bytes: len(blk.Text)}
	sum := sha256.Sum256([]byte(blk.Text))
	fb.Sha16 = hex.EncodeToString(sum[:8])
	lines := strings.Split(blk.Text, "\n")
	fb.Lines = len(lines)
	first := strings.Join(strings.Fields(lines[0]), " ")
	if len(first) > 160 {
		first = first[:160]
	}
	fb.FirstLn = first
	if len(lines) > 64 {
		h := sha256.New()
		for i := 0; i < len(lines); i += 64 {
			cs := sha256.Sum256([]byte(lines[i]))
			h.Write(cs[:4])
		}
		fb.LineMap = hex.EncodeToString(h.Sum(nil))[:16]
	}
	return fb
}

func fingerprintBlocks(msg provider.Message) []prefixDriftBlock {
	out := make([]prefixDriftBlock, 0, len(msg.Content))
	for i, blk := range msg.Content {
		out = append(out, blockFingerprint(i, blk))
	}
	return out
}

// firstDiffBracket: 1-based line number of the first differing line (-1 if
// equal) plus capped context snippets around it from both sides.
func firstDiffBracket(a, b string) (int, string, string) {
	if a == b {
		return -1, "", ""
	}
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	n := len(al)
	if len(bl) < n {
		n = len(bl)
	}
	bad := -1
	for i := 0; i < n; i++ {
		if al[i] != bl[i] {
			bad = i
			break
		}
	}
	if bad == -1 {
		bad = n
	}
	lo := bad - 3
	if lo < 0 {
		lo = 0
	}
	cap := func(lines []string) string {
		hi := bad + 6
		if hi > len(lines) {
			hi = len(lines)
		}
		var sb strings.Builder
		for _, ln := range lines[lo:hi] {
			trim := ln
			if len(trim) > 200 {
				trim = trim[:200] + "…"
			}
			sb.WriteString(trim)
			sb.WriteString("\n")
			if sb.Len() > 2000 {
				break
			}
		}
		return sb.String()
	}
	return bad + 1, cap(al), cap(bl)
}

func prefixDriftWrite(rec prefixDriftRecord) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".ggcode", prefixDriftDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	prefixDriftMu.Lock()
	defer prefixDriftMu.Unlock()
	if prefixDriftFile == nil {
		if err := openPrefixDriftFileLocked(dir); err != nil {
			return
		}
	}
	if st, err := prefixDriftFile.Stat(); err == nil && st.Size() > prefixDriftMaxBytes {
		_ = prefixDriftFile.Close()
		prefixDriftFile = nil
		if err := openPrefixDriftFileLocked(dir); err != nil {
			return
		}
	}
	rec.Seq = int(prefixDriftSeq.Add(1))
	rec.Time = time.Now().Format(time.RFC3339Nano)
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_, _ = prefixDriftFile.Write(append(b, '\n'))
}

func openPrefixDriftFileLocked(dir string) error {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	for i := 0; i+prefixDriftKeepFiles <= len(names); i++ {
		victim := filepath.Join(dir, names[i])
		if victim == prefixDriftPath {
			// #5893 audit finding: never remove the file this process still
			// holds open - cross-process rotation used to unlink live sinks
			// (data silently lost to a dangling fd).
			continue
		}
		_ = os.Remove(victim)
	}
	// Timestamp-major naming (drift-<unixts>-<pid>) so lexicographic order
	// approximates age order across processes; the pid-major form used to
	// let a long-lived low-pid process's active file sort "oldest".
	path := filepath.Join(dir, fmt.Sprintf("drift-%d-%d.jsonl", time.Now().Unix(), os.Getpid()))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	prefixDriftFile = f
	prefixDriftPath = path
	return nil
}

// recordPrefixDrift captures the fingerprint pair for a head rewrite/insert.
// Called under m.mu; allocation-light when disabled (anchor stat only).
func recordPrefixDrift(event string, old, new provider.Message, oldTokens, newTokens int, version, nonTailSeq int64) {
	if !prefixDriftOn() {
		return
	}
	rec := prefixDriftRecord{
		Event:         event,
		OldVersion:    version,
		NewVersion:    version + 1,
		NonTailSeq:    nonTailSeq,
		OldTokens:     oldTokens,
		NewTokens:     newTokens,
		FirstDiffLine: -1,
	}
	rec.OldBlocks = fingerprintBlocks(old)
	rec.NewBlocks = fingerprintBlocks(new)
	line, oldCtx, newCtx := firstDiffBracket(systemMessageText(old), systemMessageText(new))
	rec.FirstDiffLine = line
	rec.DiffOld = oldCtx
	rec.DiffNew = newCtx
	prefixDriftWrite(rec)
}

// prefixDriftActivePath exposes the current sink path for tests.
func prefixDriftActivePath() string {
	prefixDriftMu.Lock()
	defer prefixDriftMu.Unlock()
	return prefixDriftPath
}
