package ctxstats

// ctxstats.go: per-turn prompt-cache health sampler (#5812) - the
// memstats-mirror for context-prefix drift.
//
// GGCODE_CTX_SAMPLE=1 (or the marker file ~/.ggcode/ctx-sample) enables it,
// default OFF - the same gate shape as GGCODE_MEM_SAMPLE and the
// ~/.ggcode/mem-sample marker in internal/memreclaim. Every LLM turn of the
// main conversation loop then appends ONE JSONL record to
// ~/.ggcode/ctxstats-<pid>.jsonl:
//
//	ts                  RFC3339 UTC
//	pid                 process id (the file name carries it too; the field
//	                    keeps single-line audits self-describing, like memstats)
//	provider            provider name, when known
//	head_sha256         SHA-256 over the canonical serialization of
//	                    (system head + tools) - the stable prompt prefix
//	head_bytes          byte length of that canonical serialization
//	tail_bytes          byte length of non-system message content
//	head_tokens         token estimate of the head serialization
//	non_tail_mut_seq    Manager.nonTailMutSeq at sample time (#5808)
//	mut_sources         mutation sources recorded since the previous line
//	cache_read_tokens   provider usage for the turn
//	cache_write_tokens  provider usage for the turn
//	input_tokens        provider usage for the turn
//	hit_ratio           cacheRead / (cacheRead + cacheWrite); -1 when the
//	                    provider reported no cache traffic this turn
//	drift_event         head_sha256 != previous line's head_sha256
//	reason              expected-append | compaction | ptl-truncate |
//	                    system-rewrite | unknown
//
// Drift detection: a head hash change with no tracked mutation source that
// can explain it is UNEXPLAINED DRIFT (reason=unknown) and warns via
// debug.Log - the prefix cache is being invalidated by something the
// Manager does not track (tool churn, a system rewrite that bypasses
// UpdateFirstSystemMessage, adapter-level rewriting).
//
// Rolling KPI (the "memory >512MB line" analog): mean hit_ratio below 0.5
// over the last 10 cache-active turns on a session whose head exceeds 10k
// tokens warns once per condition episode - the cache is paying the write
// cost repeatedly without reads coming back.
//
// The same JSONL file powers fleet audits (python3 one-liner, identical to
// memstats). Dir override: GGCODE_CTX_SAMPLE_JSONL_DIR (tests, sandboxes).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// KPI thresholds (#5812).
const (
	kpiWindowTurns   = 10
	kpiMinHeadTokens = 10000
	kpiHitRatioFloor = 0.5
)

// Reason vocabulary, one per JSONL line.
const (
	ReasonExpectedAppend = "expected-append"
	ReasonCompaction     = "compaction"
	ReasonPTLTruncate    = "ptl-truncate"
	ReasonSystemRewrite  = "system-rewrite"
	ReasonUnknown        = "unknown"
)

// Record is one JSONL line. Field names are the audit contract (#5812).
type Record struct {
	TS               string   `json:"ts"`
	PID              int      `json:"pid"`
	Provider         string   `json:"provider,omitempty"`
	HeadSHA256       string   `json:"head_sha256"`
	HeadBytes        int      `json:"head_bytes"`
	TailBytes        int      `json:"tail_bytes"`
	HeadTokens       int      `json:"head_tokens"`
	NonTailMutSeq    int64    `json:"non_tail_mut_seq"`
	MutSources       []string `json:"mut_sources,omitempty"`
	CacheReadTokens  int      `json:"cache_read_tokens"`
	CacheWriteTokens int      `json:"cache_write_tokens"`
	InputTokens      int      `json:"input_tokens"`
	HitRatio         float64  `json:"hit_ratio"`
	DriftEvent       bool     `json:"drift_event"`
	Reason           string   `json:"reason"`
}

// TurnInput is everything one sample needs, gathered by the caller at the
// provider-call boundary (the agent conversation loop, after usage lands).
type TurnInput struct {
	Provider      string
	SystemHead    string                    // text blocks of messages[0] when it is the system head
	Tools         []provider.ToolDefinition // the exact tool defs sent this turn
	TailBytes     int                       // byte length of non-system message content
	NonTailMutSeq int64
	MutSources    []string // Manager.DrainMutationSources() for this turn
	Usage         provider.TokenUsage
}

// headPayload is the canonical serialization hashed as the stable prefix:
// system head text plus the tool definitions, in wire order. Struct field
// order fixes the serialization; RawMessage parameters pass through
// verbatim, so equal inputs hash equal and any change (text, tool set,
// tool order, tool schema) flips the hash.
type headPayload struct {
	System string                    `json:"system"`
	Tools  []provider.ToolDefinition `json:"tools,omitempty"`
}

func canonicalHead(system string, tools []provider.ToolDefinition) []byte {
	b, err := json.Marshal(headPayload{System: system, Tools: tools})
	if err != nil {
		// Degraded mode (invalid RawMessage parameters): hash a
		// deterministic name+description projection instead of skipping
		// the sample entirely.
		proj := make([]provider.ToolDefinition, len(tools))
		for i, t := range tools {
			proj[i] = provider.ToolDefinition{Name: t.Name, Description: t.Description}
		}
		b, _ = json.Marshal(headPayload{System: system, Tools: proj})
	}
	return b
}

// Enabled mirrors memreclaim samplingEnabled: GGCODE_CTX_SAMPLE=1/true or
// the marker file ~/.ggcode/ctx-sample. Evaluated per turn - one stat call,
// no caching, same as the memstats gate.
func Enabled() bool {
	switch os.Getenv("GGCODE_CTX_SAMPLE") {
	case "1", "true":
		return true
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".ggcode", "ctx-sample")); err == nil {
			return true
		}
	}
	return false
}

// sampler carries drift state and the KPI ring across turns. Mutex-guarded:
// the conversation loop is the only production writer, but tests interleave.
type sampler struct {
	mu        sync.Mutex
	havePrev  bool
	prevHead  string
	ring      []float64
	kpiWarned bool
}

var samplerState sampler

// reset clears drift + KPI state; the next record behaves like a first
// line. In-package tests use it for hermetic runs.
func (s *sampler) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.havePrev = false
	s.prevHead = ""
	s.ring = nil
	s.kpiWarned = false
}

// mutSourceReason maps a Manager mutation source onto the reason enum.
// Sources that only rewrite history AFTER the head (reconcile,
// orphan-cleanup, remove-last-group) cannot explain a head hash change and
// return "" - a drift with only those pending classifies as unknown.
func mutSourceReason(src string) string {
	switch src {
	case ctxpkg.MutSourceSystemRewrite, ctxpkg.MutSourceSystemInsert:
		return ReasonSystemRewrite
	case ctxpkg.MutSourceCompaction, ctxpkg.MutSourceMechanicalCompaction, ctxpkg.MutSourceUserReset:
		return ReasonCompaction
	case ctxpkg.MutSourcePTLTruncate:
		return ReasonPTLTruncate
	}
	return ""
}

// classifyDrift picks the reason for a head hash change from the mutation
// sources pending since the previous line. system-rewrite wins over
// compaction, compaction over ptl-truncate: the most head-specific source
// is the truthful explanation when several mutations fired in one window.
func classifyDrift(sources []string) string {
	found := map[string]bool{}
	for _, s := range sources {
		if r := mutSourceReason(s); r != "" {
			found[r] = true
		}
	}
	switch {
	case found[ReasonSystemRewrite]:
		return ReasonSystemRewrite
	case found[ReasonCompaction]:
		return ReasonCompaction
	case found[ReasonPTLTruncate]:
		return ReasonPTLTruncate
	}
	return ReasonUnknown
}

// SampleTurn computes the head hash, drift verdict, reason and KPI verdict
// for one LLM interaction and appends the JSONL record. No-op unless
// sampling is enabled; the caller gates too, this self-guards like the
// memstats writer.
func SampleTurn(in TurnInput) {
	if !Enabled() {
		return
	}
	writeRecord(in, true)
}

// writeRecord is SampleTurn with a force escape for in-package tests (the
// memstats writeStatsLine(ms, force) shape).
func writeRecord(in TurnInput, force bool) {
	if !force && !Enabled() {
		return
	}
	payload := canonicalHead(in.SystemHead, in.Tools)
	sum := sha256.Sum256(payload)
	rec := Record{
		TS:               time.Now().UTC().Format(time.RFC3339),
		PID:              os.Getpid(),
		Provider:         in.Provider,
		HeadSHA256:       hex.EncodeToString(sum[:]),
		HeadBytes:        len(payload),
		TailBytes:        in.TailBytes,
		HeadTokens:       ctxpkg.EstimateTokens(string(payload)),
		NonTailMutSeq:    in.NonTailMutSeq,
		MutSources:       sortedCopy(in.MutSources),
		CacheReadTokens:  in.Usage.CacheRead,
		CacheWriteTokens: in.Usage.CacheWrite,
		InputTokens:      in.Usage.InputTokens,
	}
	den := rec.CacheReadTokens + rec.CacheWriteTokens
	rec.HitRatio = -1
	if den > 0 {
		rec.HitRatio = float64(rec.CacheReadTokens) / float64(den)
	}

	var warnDrift, warnKPI bool
	var kpiAvg float64
	samplerState.mu.Lock()
	prev := samplerState.prevHead
	drift := samplerState.havePrev && prev != rec.HeadSHA256
	rec.DriftEvent = drift
	if !drift {
		rec.Reason = ReasonExpectedAppend
	} else {
		rec.Reason = classifyDrift(in.MutSources)
	}
	// KPI ring: only cache-active turns count toward the window.
	if den > 0 {
		samplerState.ring = append(samplerState.ring, rec.HitRatio)
		if len(samplerState.ring) > kpiWindowTurns {
			samplerState.ring = samplerState.ring[len(samplerState.ring)-kpiWindowTurns:]
		}
	}
	kpiHot := false
	if len(samplerState.ring) == kpiWindowTurns && rec.HeadTokens > kpiMinHeadTokens {
		var total float64
		for _, v := range samplerState.ring {
			total += v
		}
		kpiAvg = total / float64(len(samplerState.ring))
		kpiHot = kpiAvg < kpiHitRatioFloor
	}
	if kpiHot {
		if !samplerState.kpiWarned {
			samplerState.kpiWarned = true
			warnKPI = true
		}
	} else {
		samplerState.kpiWarned = false // condition cleared: re-arm the alert
	}
	samplerState.havePrev = true
	samplerState.prevHead = rec.HeadSHA256
	samplerState.mu.Unlock()

	if drift {
		if rec.Reason == ReasonUnknown {
			warnDrift = true
		} else {
			debug.Log("ctxstats", "prompt-cache head drift: %s -> %s reason=%s", shortHash(prev), shortHash(rec.HeadSHA256), rec.Reason)
		}
	}
	if warnDrift {
		debug.Log("ctxstats", "WARN: prompt-cache head drift with no tracked mutation source: %s -> %s non_tail_mut_seq=%d (tools churn? system rewrite outside UpdateFirstSystemMessage?)",
			shortHash(prev), shortHash(rec.HeadSHA256), rec.NonTailMutSeq)
	}
	if warnKPI {
		debug.Log("ctxstats", "WARN: prompt-cache hit_ratio %.2f over last %d turns with head %d tokens - cache is paying write repeatedly",
			kpiAvg, kpiWindowTurns, rec.HeadTokens)
	}

	appendRecord(rec)
}

func sortedCopy(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// appendRecord mirrors the memstats JSONL writer: marker-gated upstream,
// interval-independent, one append per line.
func appendRecord(rec Record) {
	dir := os.Getenv("GGCODE_CTX_SAMPLE_JSONL_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir = filepath.Join(home, ".ggcode")
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("ctxstats-%d.jsonl", os.Getpid()))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}
