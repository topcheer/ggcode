package session

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestLiveSessionContextAudit audits REAL session JSONL files for context
// reconstruction shrinkage (the "old session loads with only a few K left" bug).
//
// Safety: it only ever reads the originals and works on COPIES in t.TempDir()
// (Load may rewrite files via migrateMessageIDs / backfillTimestamps).
//
// Opt-in: GGCODE_LIVE_SESSION_AUDIT=1
// Optional: GGCODE_SESSION_DIR (default ~/.ggcode/sessions)
//
//	GGCODE_AUDIT_DAYS   (default 7)
func TestLiveSessionContextAudit(t *testing.T) {
	if os.Getenv("GGCODE_LIVE_SESSION_AUDIT") != "1" {
		t.Skip("set GGCODE_LIVE_SESSION_AUDIT=1 to audit real session files")
	}
	srcDir := os.Getenv("GGCODE_SESSION_DIR")
	if srcDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		srcDir = filepath.Join(home, ".ggcode", "sessions")
	}
	days := 7
	if d := os.Getenv("GGCODE_AUDIT_DAYS"); d != "" {
		fmt.Sscanf(d, "%d", &days)
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -days)

	// Pass 1: scan originals read-only for raw stats.
	audited := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		if info.Size() > 60*1024*1024 {
			t.Logf("%s: SKIP (>60MB, %d MB)", e.Name(), info.Size()>>20)
			continue
		}
		audited++
		auditSessionFile(t, src)
	}
	t.Logf("audited %d session files (window=%dd, dir=%s)", audited, days, srcDir)
}

func auditSessionFile(t *testing.T, src string) {
	name := filepath.Base(src)
	id := strings.TrimSuffix(name, ".jsonl")

	// ── Raw scan (read-only) ──
	f, err := os.Open(src)
	if err != nil {
		t.Logf("%s: OPEN ERR %v", name, err)
		return
	}
	var (
		diskMsgs     int
		diskMsgBytes int64
		checkpoints  []jsonlRecord
		msgLineIdx   = map[string]int{}
		emptyIDMsgs  int
		dupIDMsgs    int
		lineIdx      int
		lastMsgID    string
	)
	dec := func(line string) *jsonlRecord {
		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil
		}
		return &rec
	}
	// simple line loop
	buf, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		t.Logf("%s: READ ERR %v", name, err)
		return
	}
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rec := dec(line)
		if rec == nil {
			continue
		}
		switch rec.Type {
		case "message":
			diskMsgs++
			diskMsgBytes += int64(len(line))
			if rec.Message != nil {
				if rec.Message.ID == "" {
					emptyIDMsgs++
				} else {
					if _, dup := msgLineIdx[rec.Message.ID]; dup {
						dupIDMsgs++
					}
					msgLineIdx[rec.Message.ID] = lineIdx
					lastMsgID = rec.Message.ID
				}
			}
		case "checkpoint":
			checkpoints = append(checkpoints, *rec)
		}
		lineIdx++
	}

	// ── Load a COPY through the production path ──
	tmp := t.TempDir()
	dst := filepath.Join(tmp, name)
	if err := copyFileForAudit(src, dst); err != nil {
		t.Logf("%s: COPY ERR %v", name, err)
		return
	}
	store, err := NewJSONLStore(tmp)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ses, err := store.Load(id)
	if err != nil {
		t.Logf("%s: LOAD ERR %v", name, err)
		return
	}

	var ctxBytes int64
	var firstRole, firstSnippet string
	for i, m := range ses.ContextMessages {
		for _, b := range m.Content {
			if b.Type == "text" {
				ctxBytes += int64(len(b.Text))
			}
		}
		if i == 0 {
			firstRole = m.Role
			firstSnippet = firstTextSnippet(m, 100)
		}
	}

	// ── Classify reconstruction path ──
	path := "no-checkpoint"
	var cpInfo string
	if n := len(checkpoints); n > 0 {
		cp := checkpoints[n-1]
		cpInfo = fmt.Sprintf("cp#%d summary_msg_id=%q last_msg_id=%q tokens=%d",
			n, cp.CheckpointSummaryMsgID, cp.CheckpointLastMsgID, cp.CheckpointTokens)
		// The LAST checkpoint drives restore; if it is still legacy-format,
		// the restore path is the months-old migration branch — a bug for
		// current sessions. Historical legacy records deeper in the file are
		// fine (they predate migration and are no longer referenced).
		if cp.CheckpointSummaryMsgID == "" && len(cp.CheckpointMessages) > 0 {
			t.Errorf("%s: BUG last checkpoint is legacy-format — restore would use migration-era path", name)
		}
		if cp.CheckpointSummaryMsgID != "" {
			sumIdx, sumFound := msgLineIdx[cp.CheckpointSummaryMsgID]
			lastIdx, lastFound := msgLineIdx[cp.CheckpointLastMsgID]
			switch {
			case !sumFound:
				path = "SUMMARY-NOT-FOUND(fallback:MaxContextMessages)"
			case cp.CheckpointLastMsgID == "":
				path = "no-last_msg_id(post-summary)"
			case !lastFound:
				path = "LAST_MSG_NOT_FOUND(post-summary fallback)"
			case sumIdx < lastIdx:
				path = fmt.Sprintf("normal(summary@%d < last@%d, msgs after)", sumIdx, lastIdx)
			default:
				// summary appears AFTER last_msg_id in the file (async precompact
				// writes the summary at the tail). The loader must search the
				// FULL entry list for last_msg_id; a summary-anchored search
				// misses it and drops the messages between last and summary.
				path = fmt.Sprintf("SUMMARY-AFTER-LAST(summary@%d > last@%d)→full-list search required", sumIdx, lastIdx)
			}
		} else if len(cp.CheckpointMessages) > 0 {
			path = "legacy-checkpoint"
		}
	}

	shrink := ""
	if diskMsgs > 50 && len(ses.ContextMessages) < 8 {
		shrink = " ⚠ SHRINKAGE (few-K symptom)"
	}
	t.Logf("%s: disk_msgs=%d disk_msg_bytes=%d last_disk_msg=%q ctx_msgs=%d ctx_text_bytes=%d cp=(%s) path=%s empty_id=%d dup_id=%d first=(%s|%s)%s",
		name, diskMsgs, diskMsgBytes, lastMsgID, len(ses.ContextMessages), ctxBytes, cpInfo, path, emptyIDMsgs, dupIDMsgs, firstRole, firstSnippet, shrink)
}

func firstTextSnippet(m provider.Message, n int) string {
	for _, b := range m.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			s := strings.ReplaceAll(b.Text, "\n", " ")
			if len(s) > n {
				return s[:n]
			}
			return s
		}
	}
	return ""
}

func copyFileForAudit(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
