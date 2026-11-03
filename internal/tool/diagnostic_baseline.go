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

const baselineDiagTimeout = 150 * time.Millisecond

type diagEntry struct {
	severity int
	message  string
}

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

func (dc diagCounts) remaining(key diagEntry) bool {
	if c, ok := dc[key]; ok && c > 0 {
		dc[key]--
		return true
	}
	return false
}

type baselineSnapshot struct {
	counts diagCounts
	total  int
}

var diagBaselineMu sync.Mutex

var diagBaselines = map[string]baselineSnapshot{}

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

func diffAgainstBaseline(filePath string, current []lsp.Diagnostic) (newDiags []lsp.Diagnostic, resolvedCount int, hasBaseline bool) {
	diagBaselineMu.Lock()
	snap, ok := diagBaselines[filePath]
	delete(diagBaselines, filePath)
	diagBaselineMu.Unlock()

	if !ok {
		return current, 0, false
	}
	hasBaseline = true

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

	for _, v := range remaining {
		resolvedCount += v
	}

	return newDiags, resolvedCount, true
}

func ClearDiagnosticBaseline(filePath string) {
	diagBaselineMu.Lock()
	delete(diagBaselines, filePath)
	diagBaselineMu.Unlock()
}

func formatNewDiagnostics(newDiags []lsp.Diagnostic, resolvedCount int) string {
	if len(newDiags) == 0 && resolvedCount == 0 {
		return ""
	}

	var b strings.Builder

	if len(newDiags) > 0 {
		var errors []string
		var warnings []string
		var infos []string
		for _, d := range newDiags {
			msg := strings.TrimSpace(d.Message)
			if msg == "" {
				continue
			}
			line := d.Range.Start.Line + 1
			formatted := fmt.Sprintf("  L%d: %s", line, msg)

			switch d.Severity {
			case 1, 0:
				errors = append(errors, formatted)
			case 2:
				warnings = append(warnings, formatted)
			case 3, 4:
				infos = append(infos, formatted)
			}
		}

		if len(errors) > 0 || len(warnings) > 0 || len(infos) > 0 {
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
			if len(infos) > 0 {
				if len(errors) > 0 || len(warnings) > 0 {
					b.WriteString("\n")
				}
				b.WriteString(fmt.Sprintf("New info/hints (%d):\n", len(infos)))
				shown := infos
				if len(shown) > 5 {
					shown = shown[:5]
				}
				for _, w := range shown {
					b.WriteString(w)
					b.WriteString("\n")
				}
				if len(infos) > 5 {
					b.WriteString(fmt.Sprintf("  ... and %d more\n", len(infos)-5))
				}
			}
			if len(errors) > 0 || len(warnings) > 0 {
				b.WriteString("Fix these new issues to ensure the code compiles correctly.")
			}
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
