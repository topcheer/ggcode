package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Edit-fixer hook (arXiv 2609.00006 "Harness Engineering" pattern: repair
// failed search-and-replace edits with a cheap model call instead of
// bouncing the whole re-read-and-retry burden back to the main loop).
//
// The tool package stays provider-agnostic: the agent layer wires an
// EditFixerFunc via SetEditFixer at session start. All safety gates live
// here so the repairer's output goes through exactly the same matching and
// uniqueness pipeline as a model-supplied old_text.

// EditFixRequest carries the context an external repairer needs to correct
// a failed edit_file old_text.
type EditFixRequest struct {
	FilePath    string
	OldText     string
	NewText     string
	FileExcerpt string // numbered excerpt around the nearest matching region
}

// EditFixerFunc attempts to repair old_text so that it matches the file
// (byte-for-byte, or via the existing fallback transforms). Returns the
// corrected old_text and true on success; ("", false) means "no confident
// correction" and the original diagnostic error is returned unchanged.
type EditFixerFunc func(ctx context.Context, req EditFixRequest) (string, bool)

const (
	// editFixMaxOldText bounds the prompt size: absurdly large old_text is
	// rarely repairable and would dominate the repair call's input tokens.
	editFixMaxOldText = 8 * 1024
	// editFixMaxFile: skip repair on huge files; the excerpt alone would be
	// too lossy for a confident correction.
	editFixMaxFile = 512 * 1024
	// editFixMaxCorr: a "corrected" old_text larger than this is almost
	// certainly a runaway response, not a patch anchor.
	editFixMaxCorr = 16 * 1024
	// editFixMaxCalls bounds repair spend per process (each call is one
	// cheap, small-output completion).
	editFixMaxCalls = 32
	// editFixTimeout caps the repair call so a hung provider cannot hold a
	// write-path lock (EditFile.Execute serializes per path) forever.
	editFixTimeout = 30 * time.Second
)

var (
	editFixerMu sync.RWMutex
	editFixerFn EditFixerFunc
	// editFixerCalls counts repair attempts across the process so a hostile
	// or buggy repairer cannot turn every failed edit into an LLM spend.
	editFixerCalls atomic.Int64
	// editFixMemo remembers (path, old_text) pairs whose repair already
	// failed or was attempted, preventing retry loops when the model keeps
	// sending the same stale old_text (complements the r91 read-repeat
	// guard, which watches reads rather than edits).
	editFixMemoMu sync.Mutex
	editFixMemo   = map[string]struct{}{}
)

// SetEditFixer installs (or with nil, removes) the process-wide edit
// repairer. Called by the agent layer once per session; the last wire wins
// (sub-agents rewire to their inherited provider).
func SetEditFixer(fn EditFixerFunc) {
	editFixerMu.Lock()
	defer editFixerMu.Unlock()
	editFixerFn = fn
}

// editFixerDisabled honors the GGCODE_EDIT_FIXER kill switch
// ("0"/"false"/"off" disable repair without a restart of config plumbing).
func editFixerDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GGCODE_EDIT_FIXER"))) {
	case "0", "false", "off":
		return true
	}
	return false
}

// tryFixOldText runs the gated repair pipeline for a failed match. Returns
// the corrected old_text ("" when repair is unavailable, out of budget,
// already attempted for this input, or produced nothing usable).
func tryFixOldText(ctx context.Context, filePath, content, oldText, newText string) string {
	if editFixerDisabled() {
		return ""
	}
	editFixerMu.RLock()
	fn := editFixerFn
	editFixerMu.RUnlock()
	if fn == nil {
		return ""
	}
	if oldText == "" || len(oldText) > editFixMaxOldText || len(content) > editFixMaxFile {
		return ""
	}
	if !editFixMemoRemember(editFixMemoKey(filePath, oldText)) {
		return "" // this exact failing edit was already sent to repair once
	}
	if editFixerCalls.Add(1) > editFixMaxCalls {
		return ""
	}

	fctx, cancel := context.WithTimeout(ctx, editFixTimeout)
	defer cancel()
	corrected, ok := fn(fctx, EditFixRequest{
		FilePath:    filePath,
		OldText:     oldText,
		NewText:     newText,
		FileExcerpt: buildFixExcerpt(content, oldText),
	})
	if !ok || corrected == "" || corrected == oldText || len(corrected) > editFixMaxCorr {
		return ""
	}
	return corrected
}

func editFixMemoKey(filePath, oldText string) string {
	h := sha256.Sum256([]byte(filePath + "\x00" + oldText))
	return hex.EncodeToString(h[:])
}

// editFixMemoRemember records key and reports whether it is new. The memo
// is capped; on overflow it resets (worst case a few extra repair calls,
// never unbounded growth).
func editFixMemoRemember(key string) bool {
	editFixMemoMu.Lock()
	defer editFixMemoMu.Unlock()
	if _, dup := editFixMemo[key]; dup {
		return false
	}
	if len(editFixMemo) >= 128 {
		editFixMemo = map[string]struct{}{}
	}
	editFixMemo[key] = struct{}{}
	return true
}

// buildFixExcerpt renders a numbered window of the file centered on the
// region closest to old_text, so the repairer sees the real bytes (with
// line numbers usable as anchors) without reading the whole file. Falls
// back to the head of the file when no similar region exists.
func buildFixExcerpt(content, oldText string) string {
	lines := strings.Split(content, "\n")
	const maxLines = 300
	center := 0
	if nearest := findNearestLines(lines, oldText, 1); len(nearest) > 0 {
		center = nearest[0].lineNum - 1
	} else {
		// No similar line at all: bias toward the middle, where most edits
		// are not, but at least neither head- nor tail-biased.
		center = len(lines) / 2
	}
	start := center - maxLines/2
	if start < 0 {
		start = 0
	}
	end := start + maxLines
	if end > len(lines) {
		end = len(lines)
		start = end - maxLines
		if start < 0 {
			start = 0
		}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("(lines %d-%d of %d)\n", start+1, end, len(lines)))
	size := 0
	for i := start; i < end; i++ {
		row := fmt.Sprintf("%d\t%s\n", i+1, lines[i])
		if size+len(row) > 32*1024 {
			b.WriteString("... (excerpt truncated)\n")
			break
		}
		b.WriteString(row)
		size += len(row)
	}
	return strings.TrimRight(b.String(), "\n")
}
