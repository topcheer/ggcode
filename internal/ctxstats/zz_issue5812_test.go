package ctxstats

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// issue5812Dir resets the sampler state and points the JSONL writer at a
// per-test temp dir.
func issue5812Dir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GGCODE_CTX_SAMPLE_JSONL_DIR", dir)
	t.Setenv("GGCODE_CTX_SAMPLE", "")
	t.Setenv("HOME", t.TempDir())
	samplerState.reset()
	return dir
}

func issue5812Path() string {
	return filepath.Join(os.Getenv("GGCODE_CTX_SAMPLE_JSONL_DIR"), fmt.Sprintf("ctxstats-%d.jsonl", os.Getpid()))
}

func issue5812Read(t *testing.T) []Record {
	t.Helper()
	b, err := os.ReadFile(issue5812Path())
	if err != nil {
		t.Fatalf("read ctxstats jsonl: %v", err)
	}
	var recs []Record
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad jsonl line %q: %v", line, err)
		}
		recs = append(recs, r)
	}
	return recs
}

func issue5812ReadRaw(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(issue5812Path())
	if err != nil {
		t.Fatalf("read ctxstats jsonl: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// issue5812Capture collects ctxstats debug output for the test lifetime
// (SetLiveSink fires regardless of GGCODE_DEBUG state).
func issue5812Capture(t *testing.T) *[]string {
	t.Helper()
	logs := &[]string{}
	debug.SetLiveSink(func(cat, msg string) {
		// the sink receives the resolved category, not the raw tag; the
		// rendered message carries the [ctxstats] prefix instead.
		if strings.HasPrefix(msg, "[ctxstats]") {
			*logs = append(*logs, msg)
		}
	})
	t.Cleanup(func() { debug.SetLiveSink(nil) })
	return logs
}

func issue5812CountSubstring(logs *[]string, sub string) int {
	n := 0
	for _, m := range *logs {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

func issue5812Usage(read, write int) provider.TokenUsage {
	return provider.TokenUsage{InputTokens: read + write, CacheRead: read, CacheWrite: write}
}

// TestIssue5812AppendOnlyKeepsHeadConstant pins the core regression: an
// append-only session (no non-tail mutations, same head + same tools)
// writes identical head_sha256 on every line, no drift, reason
// expected-append, and hit_ratio is cacheRead/(cacheRead+cacheWrite).
func TestIssue5812AppendOnlyKeepsHeadConstant(t *testing.T) {
	issue5812Dir(t)
	tools := []provider.ToolDefinition{{Name: "read_file", Description: "read a file"}}
	for i := 0; i < 5; i++ {
		writeRecord(TurnInput{SystemHead: "STABLE SESSION HEAD", Tools: tools, TailBytes: 100 + i*10, NonTailMutSeq: 7, Usage: issue5812Usage(8000, 2000)}, true)
	}
	recs := issue5812Read(t)
	if len(recs) != 5 {
		t.Fatalf("want 5 lines, got %d", len(recs))
	}
	for i, r := range recs {
		if r.HeadSHA256 != recs[0].HeadSHA256 {
			t.Fatalf("line %d: append-only session must keep head_sha256 constant", i)
		}
		if r.DriftEvent {
			t.Fatalf("line %d: append-only session must not flag drift", i)
		}
		if r.Reason != ReasonExpectedAppend {
			t.Fatalf("line %d: reason = %q, want expected-append", i, r.Reason)
		}
		if r.NonTailMutSeq != 7 {
			t.Fatalf("line %d: non_tail_mut_seq = %d, want 7", i, r.NonTailMutSeq)
		}
	}
	if recs[0].HitRatio != 0.8 {
		t.Fatalf("hit_ratio = %v, want 0.8", recs[0].HitRatio)
	}
	if recs[4].TailBytes != 140 {
		t.Fatalf("tail_bytes = %d, want 140", recs[4].TailBytes)
	}
	if recs[0].CacheReadTokens != 8000 || recs[0].CacheWriteTokens != 2000 || recs[0].InputTokens != 10000 {
		t.Fatalf("usage fields wrong: %+v", recs[0])
	}
}

// TestIssue5812DeliberateHeadChangeFlipsDriftWithReason pins the second
// regression ask: a deliberate head change flips drift_event with the
// system-rewrite reason, and the new head becomes the stable baseline
// afterwards (no drift on the following append-only turn).
func TestIssue5812DeliberateHeadChangeFlipsDriftWithReason(t *testing.T) {
	issue5812Dir(t)
	logs := issue5812Capture(t)
	tools := []provider.ToolDefinition{{Name: "read_file", Description: "read a file"}}
	writeRecord(TurnInput{SystemHead: "HEAD VERSION ONE", Tools: tools, NonTailMutSeq: 1, Usage: issue5812Usage(100, 9900)}, true)
	writeRecord(TurnInput{SystemHead: "HEAD VERSION TWO", Tools: tools, NonTailMutSeq: 2, MutSources: []string{ctxpkg.MutSourceSystemRewrite}, Usage: issue5812Usage(200, 9800)}, true)
	writeRecord(TurnInput{SystemHead: "HEAD VERSION TWO", Tools: tools, NonTailMutSeq: 2, Usage: issue5812Usage(9000, 1000)}, true)
	recs := issue5812Read(t)
	if len(recs) != 3 {
		t.Fatalf("want 3 lines, got %d", len(recs))
	}
	if !recs[1].DriftEvent {
		t.Fatal("deliberate head change must flip drift_event")
	}
	if recs[1].Reason != ReasonSystemRewrite {
		t.Fatalf("reason = %q, want system-rewrite", recs[1].Reason)
	}
	if recs[1].HeadSHA256 == recs[0].HeadSHA256 {
		t.Fatal("head hash must change with the head text")
	}
	if recs[2].DriftEvent {
		t.Fatal("turn after the change must be drift-free (new baseline)")
	}
	if recs[2].Reason != ReasonExpectedAppend {
		t.Fatalf("reason = %q, want expected-append", recs[2].Reason)
	}
	if issue5812CountSubstring(logs, "WARN") != 0 {
		t.Fatalf("explained drift must not WARN, got: %v", *logs)
	}
}

// TestIssue5812ReasonClassificationMatrix walks the full reason table:
// compaction-family sources classify a drift as compaction, ptl-truncate
// as ptl-truncate, system-insert as system-rewrite, and history-only
// sources (reconcile) classify as unknown because they cannot change the
// head hash.
func TestIssue5812ReasonClassificationMatrix(t *testing.T) {
	cases := []struct {
		name    string
		sources []string
		want    string
	}{{
		name:    "compaction",
		sources: []string{ctxpkg.MutSourceCompaction},
		want:    ReasonCompaction,
	}, {
		name:    "mechanical-compaction is compaction family",
		sources: []string{ctxpkg.MutSourceMechanicalCompaction},
		want:    ReasonCompaction,
	}, {
		name:    "user-reset is compaction family",
		sources: []string{ctxpkg.MutSourceUserReset},
		want:    ReasonCompaction,
	}, {
		name:    "ptl-truncate",
		sources: []string{ctxpkg.MutSourcePTLTruncate},
		want:    ReasonPTLTruncate,
	}, {
		name:    "system-insert is system-rewrite",
		sources: []string{ctxpkg.MutSourceSystemInsert},
		want:    ReasonSystemRewrite,
	}, {
		name:    "system-rewrite beats ptl-truncate",
		sources: []string{ctxpkg.MutSourcePTLTruncate, ctxpkg.MutSourceSystemRewrite},
		want:    ReasonSystemRewrite,
	}, {
		name:    "history-only reconcile cannot explain head drift",
		sources: []string{ctxpkg.MutSourceReconcile},
		want:    ReasonUnknown,
	}, {
		name:    "no sources at all",
		sources: nil,
		want:    ReasonUnknown,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issue5812Dir(t)
			writeRecord(TurnInput{SystemHead: "HEAD A", NonTailMutSeq: 1, Usage: issue5812Usage(10, 10)}, true)
			writeRecord(TurnInput{SystemHead: "HEAD B", NonTailMutSeq: 2, MutSources: tc.sources, Usage: issue5812Usage(10, 10)}, true)
			recs := issue5812Read(t)
			if len(recs) != 2 || !recs[1].DriftEvent {
				t.Fatalf("want drift on line 2, got %+v", recs)
			}
			if recs[1].Reason != tc.want {
				t.Fatalf("reason = %q, want %q", recs[1].Reason, tc.want)
			}
		})
	}
}

// TestIssue5812UnknownDriftWarns pins the alert face: drift with reason
// unknown logs exactly one WARN naming the unexplained hash flip, while
// the same drift with a tracked source stays silent.
func TestIssue5812UnknownDriftWarns(t *testing.T) {
	issue5812Dir(t)
	logs := issue5812Capture(t)
	writeRecord(TurnInput{SystemHead: "HEAD A", NonTailMutSeq: 1, Usage: issue5812Usage(10, 10)}, true)
	writeRecord(TurnInput{SystemHead: "HEAD B", NonTailMutSeq: 2, MutSources: nil, Usage: issue5812Usage(10, 10)}, true)
	if n := issue5812CountSubstring(logs, "WARN"); n != 1 {
		t.Fatalf("unknown drift must WARN exactly once, got %d: %v", n, *logs)
	}
	if !strings.Contains((*logs)[0], "no tracked mutation source") {
		t.Fatalf("warn text must name the unexplained drift: %q", (*logs)[0])
	}
}

// TestIssue5812HitRatioKPIWindow pins the rolling KPI: mean hit_ratio
// below 0.5 over the last 10 cache-active turns on a head above 10k
// tokens warns once per episode, re-arms after the window recovers, and
// never fires for a small head.
func TestIssue5812HitRatioKPIWindow(t *testing.T) {
	issue5812Dir(t)
	logs := issue5812Capture(t)
	bigHead := strings.Repeat("h", 40000) // ~11.4k estimated tokens
	bad := issue5812Usage(100, 900)       // 0.1
	good := issue5812Usage(9000, 1000)    // 0.9
	for i := 0; i < 12; i++ {
		writeRecord(TurnInput{SystemHead: bigHead, Usage: bad}, true)
	}
	if n := issue5812CountSubstring(logs, "cache is paying write repeatedly"); n != 1 {
		t.Fatalf("episode 1: want exactly 1 KPI warn after 12 bad turns, got %d: %v", n, *logs)
	}
	for i := 0; i < 10; i++ {
		writeRecord(TurnInput{SystemHead: bigHead, Usage: good}, true)
	}
	for i := 0; i < 10; i++ {
		writeRecord(TurnInput{SystemHead: bigHead, Usage: bad}, true)
	}
	if n := issue5812CountSubstring(logs, "cache is paying write repeatedly"); n != 2 {
		t.Fatalf("episode 2: KPI warn must re-arm after recovery, got %d warns: %v", n, *logs)
	}
}

// TestIssue5812HitRatioKPISmallHeadStaysQuiet: a poor ratio on a session
// whose head is under the 10k-token threshold is not a cache-health
// signal.
func TestIssue5812HitRatioKPISmallHeadStaysQuiet(t *testing.T) {
	issue5812Dir(t)
	logs := issue5812Capture(t)
	for i := 0; i < 12; i++ {
		writeRecord(TurnInput{SystemHead: "TINY HEAD", Usage: issue5812Usage(100, 900)}, true)
	}
	if n := issue5812CountSubstring(logs, "cache is paying write repeatedly"); n != 0 {
		t.Fatalf("small head must not fire the KPI warn, got %d: %v", n, *logs)
	}
}

// TestIssue5812JSONLContractFields pins the audit contract: the raw line
// keys match the issue field list exactly, and head_sha256 is the SHA-256
// of the canonical (system + tools) serialization computed independently
// here.
func TestIssue5812JSONLContractFields(t *testing.T) {
	issue5812Dir(t)
	tools := []provider.ToolDefinition{
		{Name: "read_file", Description: "read a file", Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "write_file", Description: "write a file"},
	}
	writeRecord(TurnInput{Provider: "anthropic", SystemHead: "CONTRACT HEAD", Tools: tools, TailBytes: 4242, NonTailMutSeq: 3, MutSources: []string{ctxpkg.MutSourceCompaction}, Usage: issue5812Usage(300, 100)}, true)
	raw := issue5812ReadRaw(t)
	if len(raw) != 1 {
		t.Fatalf("want 1 raw line, got %d", len(raw))
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw[0]), &obj); err != nil {
		t.Fatalf("line is not JSON: %v", err)
	}
	wantKeys := []string{"ts", "pid", "provider", "head_sha256", "head_bytes", "tail_bytes", "head_tokens", "non_tail_mut_seq", "mut_sources", "cache_read_tokens", "cache_write_tokens", "input_tokens", "hit_ratio", "drift_event", "reason"}
	for _, k := range wantKeys {
		if _, ok := obj[k]; !ok {
			t.Fatalf("jsonl line missing key %q: %s", k, raw[0])
		}
	}
	// independent hash pin
	type ref struct {
		System string                    `json:"system"`
		Tools  []provider.ToolDefinition `json:"tools,omitempty"`
	}
	payload, err := json.Marshal(ref{System: "CONTRACT HEAD", Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	var rec Record
	if err := json.Unmarshal([]byte(raw[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.HeadSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("head_sha256 mismatch: sampler %s, independent %s", rec.HeadSHA256, hex.EncodeToString(sum[:]))
	}
	if rec.HeadBytes != len(payload) {
		t.Fatalf("head_bytes = %d, want %d", rec.HeadBytes, len(payload))
	}
	if rec.HitRatio != 0.75 {
		t.Fatalf("hit_ratio = %v, want 0.75", rec.HitRatio)
	}
	if rec.MutSources[0] != ctxpkg.MutSourceCompaction {
		t.Fatalf("mut_sources = %v", rec.MutSources)
	}
}

// TestIssue5812GateDisabledAndMarker pins the memstats-style gate: off by
// default, on via the ~/.ggcode/ctx-sample marker file.
func TestIssue5812GateDisabledAndMarker(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("GGCODE_CTX_SAMPLE_JSONL_DIR", dir)
	t.Setenv("GGCODE_CTX_SAMPLE", "")
	t.Setenv("HOME", home)
	samplerState.reset()
	SampleTurn(TurnInput{SystemHead: "H", Usage: issue5812Usage(1, 1)})
	if _, err := os.Stat(issue5812Path()); !os.IsNotExist(err) {
		t.Fatal("sampler must be off without env or marker")
	}
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ggcode", "ctx-sample"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	SampleTurn(TurnInput{SystemHead: "H", Usage: issue5812Usage(1, 1)})
	if _, err := os.Stat(issue5812Path()); err != nil {
		t.Fatalf("marker file must enable the sampler: %v", err)
	}
}

// TestIssue5812NoCacheTrafficRatioIsMinusOne: providers that report no
// cache fields yield hit_ratio -1 (documented sentinel), excluded from
// the KPI ring.
func TestIssue5812NoCacheTrafficRatioIsMinusOne(t *testing.T) {
	issue5812Dir(t)
	writeRecord(TurnInput{SystemHead: "H", Usage: provider.TokenUsage{InputTokens: 42}}, true)
	recs := issue5812Read(t)
	if len(recs) != 1 || recs[0].HitRatio != -1 {
		t.Fatalf("hit_ratio = %+v, want -1", recs)
	}
}
