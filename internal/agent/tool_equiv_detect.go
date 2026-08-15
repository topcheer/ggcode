package agent

// Semantic Tool Call Equivalence Detector
//
// Research basis: ToolCaching (arXiv:2504.12401) and TVCACHE identify that
// traditional tool-call deduplication fails because it relies on exact argument
// matching. In practice, LLM agents frequently issue calls with:
//   - JSON keys in different order ({"a":1,"b":2} vs {"b":2,"a":1})
//   - Volatile metadata fields (trace_id, request_id, timestamp, nonce)
//   - Functionally equivalent but syntactically different representations
//
// The production caching literature (2025-2026) identifies this as the #1
// "caching accident": the cache key is too coarse on some dimensions (ignoring
// user scope) yet too narrow on others (exact byte match misses semantic
// equivalence). The recommended fix is argument normalization:
//
//   normalized_args = sorted_json(strip_volatile_fields(args))
//
// Gap in ggcode: The existing tool_redundancy.go uses fingerprintToolCall
// (loop_detect.go:46) which does sha256(name + "|" + raw_args_bytes). Two calls
// to grep with {"pattern":"foo","path":"/x"} and {"path":"/x","pattern":"foo"}
// produce DIFFERENT fingerprints, so scattered-duplicate detection misses them
// entirely. This detector fills that gap by normalizing arguments before
// comparison.
//
// Design:
//   - Normalizes JSON args: strips volatile fields, sorts keys recursively
//   - Tracks by normalized fingerprint per run
//   - Warns when the SAME normalized fingerprint appears 2+ times (lower
//     threshold than tool_redundancy.go's 3, because semantic duplicates are
//     more insidious: the agent does not realize it is repeating itself)
//   - Only fires when exact-match did not already catch it (avoid double-warning)
//   - Max 2 warnings per run (advisory, not blocking)
//   - Zero LLM cost - deterministic JSON parse + hash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	equivWarnThreshold = 3 // occurrences of normalized-equivalent call before warning (#494: was 2 — fired before tool_redundancy on byte-identical repeats, double-warning)
	equivMaxWarnings   = 2 // max warnings per run
)

// volatileFields are stripped during normalization because they do not affect
// the semantic result of the tool call. They are metadata/tracing fields that
// vary per invocation even when the actual parameters are identical.
var volatileFields = map[string]bool{
	"trace_id":       true,
	"request_id":     true,
	"timestamp":      true,
	"_t":             true,
	"nonce":          true,
	"correlation_id": true,
}

type toolEquivDetectState struct {
	normalizedCounts map[string]int    // normalized fingerprint -> count
	toolNames        map[string]string // fingerprint -> tool name (readable)
	// rawSeen/rawCount implement the exact-match suppression contract (#494):
	// rawSeen[normFp] is true while EVERY occurrence of that normalized
	// fingerprint has been byte-identical — i.e. fully covered by the
	// tool_redundancy detector, which this detector yields to.
	rawSeen  map[string]bool
	rawCount map[string]int
	warnings int
}

func newToolEquivDetectState() *toolEquivDetectState {
	return &toolEquivDetectState{
		normalizedCounts: make(map[string]int),
		toolNames:        make(map[string]string),
		rawSeen:          make(map[string]bool),
		rawCount:         make(map[string]int),
	}
}

func (s *toolEquivDetectState) reset() {
	s.normalizedCounts = make(map[string]int)
	s.toolNames = make(map[string]string)
	s.rawSeen = make(map[string]bool)
	s.rawCount = make(map[string]int)
	s.warnings = 0
}

// markExactMatch is retained as a no-op shim: raw-vs-normalized
// cross-referencing now happens per-call inside recordCall (#494).
func (s *toolEquivDetectState) markExactMatch(rawFp string) {
	_ = rawFp
}

// normalizeArgs parses JSON arguments, strips volatile fields, sorts keys,
// and returns a canonical string representation. If args is not valid JSON,
// returns the raw string (fallback — don't crash on malformed input).
func normalizeArgs(args []byte) string {
	if len(args) == 0 {
		return ""
	}
	var parsed interface{}
	if err := json.Unmarshal(args, &parsed); err != nil {
		// Not valid JSON — use raw bytes as fallback
		return string(args)
	}
	normalized := normalizeValue(parsed)
	// json.Marshal of a map produces sorted keys in Go
	out, err := json.Marshal(normalized)
	if err != nil {
		return string(args)
	}
	return string(out)
}

// normalizeValue recursively strips volatile fields from maps and ensures
// deterministic structure. Go's json.Marshal already sorts map keys, so we
// just need to remove volatile fields and recurse.
func normalizeValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, vv := range val {
			if volatileFields[k] {
				continue
			}
			result[k] = normalizeValue(vv)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = normalizeValue(item)
		}
		return result
	default:
		return v
	}
}

// recordCall tracks a tool call with normalized arguments and returns guidance
// if a semantic-equivalent duplicate pattern is detected.
// rawFp is the exact-match fingerprint (same raw bytes = same rawFp) — used to
// detect repetition fully covered by the exact-match tool_redundancy
// detector, which this detector then yields to (#494).
func (s *toolEquivDetectState) recordCall(toolName string, args []byte, rawFp string) string {
	if s.warnings >= equivMaxWarnings {
		return ""
	}

	normalized := normalizeArgs(args)
	normFp := normalizedFingerprint(toolName, normalized)

	s.normalizedCounts[normFp]++
	s.toolNames[normFp] = toolName

	// #494 exact-match suppression: if EVERY occurrence of this normalized
	// fingerprint so far was byte-identical, tool_redundancy already sees
	// the repetition — this detector yields (the documented :32 contract:
	// only fire when exact-match did NOT already catch it). Warn only once
	// an occurrence DIVERGES (reordered keys / volatile fields): that is
	// the semantic-equivalence case this detector exists for.
	rawSameFp := s.rawCount[normFp+"|"+rawFp] + 1
	s.rawCount[normFp+"|"+rawFp] = rawSameFp
	if rawSameFp == s.normalizedCounts[normFp] {
		s.rawSeen[normFp] = true
	} else {
		s.rawSeen[normFp] = false
	}

	count := s.normalizedCounts[normFp]

	if count == equivWarnThreshold && !s.rawSeen[normFp] {
		s.warnings++
		return fmt.Sprintf(
			"Semantic duplicate: You called %s %d times with equivalent arguments "+
				"(same parameters after normalizing key order and volatile fields). "+
				"The results will be identical — unless context compaction has trimmed "+
				"earlier results, you already have this information in context - "+
				"avoid re-invoking with slightly different argument formatting.",
			toolName, count,
		)
	}

	if count == equivWarnThreshold*3 && !s.rawSeen[normFp] {
		s.warnings++
		return fmt.Sprintf(
			"Warning: %s called %d times with semantically equivalent arguments. "+
				"This wastes significant iteration budget. Reformatting arguments does not change results. "+
				"Use the data you already have (unless it was trimmed by context compaction).",
			toolName, count,
		)
	}

	return ""
}

// fingerprintToolCallNormalized produces a normalized fingerprint for
// cross-referencing. Used externally if needed.
func fingerprintToolCallNormalized(name string, args []byte) string {
	normalized := normalizeArgs(args)
	return normalizedFingerprint(name, normalized)
}

func normalizedFingerprint(name, normalizedArgs string) string {
	h := sha256.Sum256([]byte(name + "|" + normalizedArgs))
	return hex.EncodeToString(h[:16])
}
