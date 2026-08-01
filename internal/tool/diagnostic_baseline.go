package tool

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/lsp"
)

// Diagnostic Baseline Diffing
//
// Problem: post-edit diagnostics show ALL issues in a file, including
// pre-existing ones that the agent didn't cause. When a file already has
// 5 warnings before the edit, the agent sees those 5 + any new ones, but
// cannot distinguish which are new vs. pre-existing. This wastes agent
// time fixing unrelated warnings and dilutes the signal-to-noise ratio.
//
// Solution: capture a diagnostic baseline BEFORE the edit is applied, then
// diff post-edit diagnostics against it. Only show NEWLY INTRODUCED issues.
// Also report resolved issues as positive feedback.
//
// Competitor mapping:
//   - Cursor: shows inline squiggles but doesn't diff pre/post
//   - Claude Code: runs go vet / type checks but shows all results
//   - Cline: runs lint in verification loop, doesn't diff
//   - VS Code: "Problems" panel shows all, but code actions focus on changed lines
//
// The baseline is captured with a very short timeout (150ms) because it
// reads the LSP server's cached diagnostics — no expensive computation.
// If the LSP server is slow to respond, we skip the baseline and fall back
// to showing all diagnostics (current behavior).

const baselineDiagTimeout = 150 * time.Millisecond

// diagEntry is a deduplication key for a diagnostic, based on severity and
// message. Line numbers are intentionally excluded because edits shift line
// numbers, which would cause all pre-existing diagnostics to appear "new"
// after a multi-line insertion. Message+severity is sufficient to identify
// whether a specific class of issue already existed.
type diagEntry struct {
	severity int
	message  string
}

// diagCounts tracks how many diagnostics of each (severity, message) exist.
// This handles the case where a file has multiple "unused variable" warnings —
// if the edit adds another one, the count increases and we detect it as new.
type diagCounts map[diagEntry]int

func newDiagCounts(diags []lsp.Diagnostic) diagCounts {
	dc := make(diagCounts, len(diags))
	for _, d := range diags {
		msg := strings.TrimSpace(d.Message)
		if msg == "" {
			continue
		}
		dc[diagEntry{severity: d.Severity, message: msg}]++
	}
	return dc
}

// remaining returns true if at least one diagnostic of this key hasn't been
// consumed yet, decrementing the count. Used to "match" post-edit diagnostics
// against the baseline.
func (dc diagCounts) remaining(key diagEntry) bool {
	if c, ok := dc[key]; ok && c > 0 {
		dc[key]--
		return true
	}
	return false
}

// baselineSnapshot stores pre-edit diagnostics for a single file.
type baselineSnapshot struct {
	counts diagCounts
	total  int // total number of diagnostics at capture time
}

// diagBaselineMu protects diagBaselines.
var diagBaselineMu sync.Mutex

// diagBaselines maps normalized file path → pre-edit diagnostic snapshot.
// Entries are consumed (deleted) when postEditDiagnostics runs.
var diagBaselines = map[string]baselineSnapshot{}

// CaptureDiagnosticBaseline captures the current LSP diagnostics for a file
// BEFORE an edit is applied. The snapshot enables post-edit diagnostic
// diffing — showing only newly introduced issues rather than all pre-existing
// ones.
//
// This function is designed to be very fast (≤150ms) because it reads the
// LSP server's cached diagnostics. If the server is slow or unavailable,
// it silently skips (no baseline → postEditDiagnostics falls back to showing
// all diagnostics).
func CaptureDiagnosticBaseline(workingDir, filePath string) {
	if !postEditDiagEnabled || workingDir == "" || filePath == "" {
		return
	}
	if !isSourceFile(filePath) {
		return
	}
	if _, ok := lsp.ResolveServerForWorkspace(workingDir); !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), baselineDiagTimeout)
	defer cancel()

	diags, err := lsp.Diagnostics(ctx, workingDir, filePath)
	if err != nil {
		debug.Log("diag-baseline", "baseline capture failed for %s: %v", filePath, err)
		return
	}

	snap := baselineSnapshot{
		counts: newDiagCounts(diags),
		total:  len(diags),
	}

	diagBaselineMu.Lock()
	diagBaselines[filePath] = snap
	diagBaselineMu.Unlock()

	debug.Log("diag-baseline", "captured baseline for %s: %d diagnostics", filePath, snap.total)
}

// diffAgainstBaseline compares post-edit diagnostics against any stored
// baseline for this file. Returns:
//   - newDiags: diagnostics that are newly introduced (not in baseline)
//   - resolvedCount: number of pre-existing diagnostics that the edit fixed
//   - hasBaseline: whether a baseline was available for diffing
//
// The baseline is consumed (deleted) after diffing.
func diffAgainstBaseline(filePath string, current []lsp.Diagnostic) (newDiags []lsp.Diagnostic, resolvedCount int, hasBaseline bool) {
	diagBaselineMu.Lock()
	snap, ok := diagBaselines[filePath]
	delete(diagBaselines, filePath) // always consume
	diagBaselineMu.Unlock()

	if !ok {
		return current, 0, false
	}
	hasBaseline = true

	// Clone baseline counts so we can decrement as we match.
	remaining := make(diagCounts, len(snap.counts))
	for k, v := range snap.counts {
		remaining[k] = v
	}

	for _, d := range current {
		msg := strings.TrimSpace(d.Message)
		if msg == "" {
			newDiags = append(newDiags, d)
			continue
		}
		key := diagEntry{severity: d.Severity, message: msg}
		if !remaining.remaining(key) {
			newDiags = append(newDiags, d)
		}
	}

	// Count resolved: sum of remaining baseline entries that weren't matched.
	for _, v := range remaining {
		resolvedCount += v
	}

	return newDiags, resolvedCount, true
}

// ClearDiagnosticBaseline removes the baseline for a file without diffing.
// Used when an edit is skipped (no-op) or fails, to avoid stale baselines.
func ClearDiagnosticBaseline(filePath string) {
	diagBaselineMu.Lock()
	delete(diagBaselines, filePath)
	diagBaselineMu.Unlock()
}

// formatNewDiagnostics formats only the NEWLY INTRODUCED diagnostics as a
// concise warning string. The output explicitly labels them as "new" so the
// agent knows these were caused by its edit, not pre-existing.
func formatNewDiagnostics(newDiags []lsp.Diagnostic, resolvedCount int) string {
	if len(newDiags) == 0 && resolvedCount == 0 {
		return ""
	}

	var b strings.Builder

	if len(newDiags) > 0 {
		var errors []string
		var warnings []string
		for _, d := range newDiags {
			msg := strings.TrimSpace(d.Message)
			if msg == "" {
				continue
			}
			line := d.Range.Start.Line + 1
			formatted := fmt.Sprintf("  L%d: %s", line, msg)
			if d.Severity <= 1 {
				errors = append(errors, formatted)
			} else if d.Severity == 2 {
				warnings = append(warnings, formatted)
			}
		}

		if len(errors) > 0 || len(warnings) > 0 {
			b.WriteString("\n\n[New Diagnostics — introduced by this edit]\n")
			if len(errors) > 0 {
				b.WriteString(fmt.Sprintf("New errors (%d):\n", len(errors)))
				shown := errors
				if len(shown) > 10 {
					shown = shown[:10]
				}
				for _, e := range shown {
					b.WriteString(e)
					b.WriteString("\n")
				}
				if len(errors) > 10 {
					b.WriteString(fmt.Sprintf("  ... and %d more\n", len(errors)-10))
				}
			}
			if len(warnings) > 0 {
				if len(errors) > 0 {
					b.WriteString("\n")
				}
				b.WriteString(fmt.Sprintf("New warnings (%d):\n", len(warnings)))
				shown := warnings
				if len(shown) > 5 {
					shown = shown[:5]
				}
				for _, w := range shown {
					b.WriteString(w)
					b.WriteString("\n")
				}
				if len(warnings) > 5 {
					b.WriteString(fmt.Sprintf("  ... and %d more\n", len(warnings)-5))
				}
			}
			b.WriteString("Fix these new issues to ensure the code compiles correctly.")
		}
	}

	if len(newDiags) == 0 && resolvedCount > 0 {
		b.WriteString(fmt.Sprintf(
			"\n\n[Diagnostic Baseline — no new issues. This edit resolved %d pre-existing diagnostic(s).]\n",
			resolvedCount,
		))
	} else if resolvedCount > 0 {
		b.WriteString(fmt.Sprintf("\n(Also resolved %d pre-existing diagnostic(s).)\n", resolvedCount))
	}

	return b.String()
}
