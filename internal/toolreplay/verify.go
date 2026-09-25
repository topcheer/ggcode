package toolreplay

// Cut-point verification: replay a recorded tape against the CURRENT tool
// implementations to detect behavior changes, à la Chronicle ("Cut-Point
// Replay for Regression Testing of LLM Agents", arXiv:2609.20625).
//
// A tape records the non-deterministic boundaries of an agent run — every
// (tool, input) → result crossing — as immutable envelopes. Verification
// walks those boundaries in order:
//
//   - boundaries [0, Cut) are SERVED from the record (never executed,
//     bit-stable);
//   - boundaries [Cut, ∞) are EXECUTED LIVE with the current code and their
//     results COMPARED against the recorded envelopes.
//
// Any divergence means the code change would have altered what the agent
// observed during the recorded incident: the verify run FAILS. This turns a
// recorded session (e.g. attached to a bug report) into a regression test
// that runs offline in CI without a single model call.
//
// Safety: by default only read-only tools (permission.IsReadOnlyTool) are
// re-executed live; mutating boundaries are reported as skipped-unsafe so
// verification can never cause the side effects (writes, subprocesses,
// network mutations) the original run had, unless --include-unsafe is given.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

// VerifyStatus is the outcome of one recorded boundary under verification.
type VerifyStatus string

const (
	// VerifyMatch: the live execution reproduced the recorded result.
	VerifyMatch VerifyStatus = "match"
	// VerifyDiverged: live behavior differs from the record. Fails the run.
	VerifyDiverged VerifyStatus = "diverged"
	// VerifyCut: below the cut point — served from the record, not executed.
	VerifyCut VerifyStatus = "cut"
	// VerifySkippedUnsafe: mutating tool not re-executed (safe default).
	VerifySkippedUnsafe VerifyStatus = "skipped-unsafe"
	// VerifyNoTool: the recorded tool is not registered in this build.
	// Fails the run because the boundary cannot be verified at all.
	VerifyNoTool VerifyStatus = "no-tool"
)

// VerifyOptions controls a cut-point verification run.
type VerifyOptions struct {
	// Cut is the number of leading boundaries served from the record
	// instead of being executed live. 0 (default) verifies everything live.
	Cut int
	// IncludeUnsafe re-executes mutating tools live as well. Use only when
	// the workspace is known to tolerate the recorded side effects.
	IncludeUnsafe bool
}

// VerifyResult is the per-boundary outcome.
type VerifyResult struct {
	Index    int             `json:"index"`
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input,omitempty"`
	Status   VerifyStatus    `json:"status"`
	Detail   string          `json:"detail,omitempty"`
}

// VerifyReport aggregates the per-boundary outcomes of one run.
type VerifyReport struct {
	TapePath string         `json:"tape_path"`
	Results  []VerifyResult `json:"results"`
}

// Passed reports whether the tape still holds against the current code:
// no diverged boundaries and no unverifiable (missing tool) boundaries.
// Cut and skipped-unsafe boundaries do not fail the run — partial coverage
// is the point of the safe default.
func (r VerifyReport) Passed() bool {
	for _, res := range r.Results {
		if res.Status == VerifyDiverged || res.Status == VerifyNoTool {
			return false
		}
	}
	return true
}

// Counts tallies outcomes by status.
func (r VerifyReport) Counts() map[VerifyStatus]int {
	counts := make(map[VerifyStatus]int, 5)
	for _, res := range r.Results {
		counts[res.Status]++
	}
	return counts
}

// EntriesInOrder returns a snapshot of the tape entries in insertion order.
// Unlike Lookup it never consumes slots, so verification leaves the tape
// usable for full replay afterwards.
func (t *Tape) EntriesInOrder() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Entry, 0, len(t.order))
	for _, s := range t.order {
		out = append(out, s.Entry)
	}
	return out
}

// Verify walks the tape boundaries against reg and returns per-boundary
// results in tape order. The tape is not modified.
func Verify(ctx context.Context, reg *tool.Registry, tape *Tape, opts VerifyOptions) ([]VerifyResult, error) {
	if reg == nil {
		return nil, fmt.Errorf("toolreplay: nil tool registry")
	}
	if tape == nil {
		return nil, fmt.Errorf("toolreplay: nil tape")
	}
	entries := tape.EntriesInOrder()
	results := make([]VerifyResult, 0, len(entries))
	for i, e := range entries {
		res := VerifyResult{Index: i, ToolName: e.ToolName, Input: e.Input}
		if i < opts.Cut {
			res.Status = VerifyCut
			res.Detail = "served from record"
			results = append(results, res)
			continue
		}
		impl, ok := reg.Get(e.ToolName)
		if !ok {
			res.Status = VerifyNoTool
			res.Detail = fmt.Sprintf("tool %q is not registered in this build", e.ToolName)
			results = append(results, res)
			continue
		}
		if !opts.IncludeUnsafe && !permission.IsReadOnlyTool(e.ToolName) {
			res.Status = VerifySkippedUnsafe
			res.Detail = "mutating tool; use --include-unsafe to execute live"
			results = append(results, res)
			continue
		}
		live, liveErr := impl.Execute(ctx, json.RawMessage(e.Input))
		res.Status, res.Detail = compareBoundary(e, live, liveErr)
		results = append(results, res)
	}
	return results, nil
}

// compareBoundary classifies one live execution against its recorded
// envelope. Error-ness is compared first (outcome class, per Chronicle's
// guarded-tool assertions): if the record errored and live did not — or
// vice versa — the boundary diverged. Two errors count as a match: error
// wording legitimately shifts across builds, the outcome class is what the
// regression test asserts. Successful executions compare normalized content.
func compareBoundary(recorded Entry, live tool.Result, liveErr error) (VerifyStatus, string) {
	recFailed := recorded.Err != "" || recorded.Result.IsError
	liveFailed := liveErr != nil || live.IsError
	switch {
	case recFailed && liveFailed:
		return VerifyMatch, fmt.Sprintf("both recorded and live runs errored (recorded: %s)",
			firstLine(orDefault(recorded.Err, recorded.Result.Content)))
	case recFailed && !liveFailed:
		return VerifyDiverged, fmt.Sprintf("recorded run errored (%s) but live execution succeeded",
			firstLine(orDefault(recorded.Err, recorded.Result.Content)))
	case !recFailed && liveFailed:
		liveText := live.Content
		if liveErr != nil {
			liveText = liveErr.Error()
		}
		return VerifyDiverged, fmt.Sprintf("recorded run succeeded but live execution errored (%s)",
			firstLine(liveText))
	}
	if normalizeContent(recorded.Result.Content) != normalizeContent(live.Content) {
		return VerifyDiverged, fmt.Sprintf("content differs (recorded %d B, live %d B)",
			len(recorded.Result.Content), len(live.Content))
	}
	return VerifyMatch, ""
}

// normalizeContent reduces incidental formatting noise before comparison:
// CRLF line endings collapse to LF and trailing whitespace is trimmed, so a
// reader that starts emitting a trailing newline does not fail the test.
func normalizeContent(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimRight(s, " \t\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
