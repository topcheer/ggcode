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
	"strings"
)

const (
	equivWarnThreshold = 3 // occurrences of normalized-equivalent call before warning (#494: was 2 — fired before tool_redundancy on byte-identical repeats, double-warning)
	equivMaxWarnings   = 2 // max warnings per run
)

// volatileFields are stripped during normalization because they do not affect
// the semantic result of the tool call. They are metadata/tracing fields that
// vary per invocation even when the actual parameters are identical.
//
// #3631: stripping is scoped to builtin (non-MCP) tools only. MCP tools
// frequently use names like "timestamp" as *semantic* parameters (log time
// ranges, DB query windows, monitoring intervals) where the value carries
// meaning. Their schema is not knowable here, so we err on the side of NOT
// stripping: a missed advisory (false negative) is cheap, while a false
// "results will be identical" assertion can trick the agent into skipping a
// genuinely needed query for a different time window.
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

// mcpToolNamePrefix mirrors internal/tool's MCP adapter naming
// (mcp__server__tool). MCP tool argument semantics are unknowable to builtin
// detectors, so volatile-field stripping is disabled for them (#3631).
const mcpToolNamePrefix = "mcp__"

// stripVolatileForTool reports whether volatile-field stripping should apply
// to the given tool. Only builtin tools (whose parameter schemas are known to
// lack timestamp-style semantic fields) are stripped; MCP tools are not.
func stripVolatileForTool(toolName string) bool {
	return !strings.HasPrefix(toolName, mcpToolNamePrefix)
}

// normalizeArgs parses JSON arguments, sorts keys, and — for builtin tools
// only (#3631) — strips volatile fields, returning a canonical string
// representation. If args is not valid JSON, returns the raw string
// (fallback — don't crash on malformed input).
func normalizeArgs(toolName string, args []byte) string {
	if len(args) == 0 {
		return ""
	}
	var parsed interface{}
	if err := json.Unmarshal(args, &parsed); err != nil {
		// Not valid JSON — use raw bytes as fallback
		return string(args)
	}
	normalized := normalizeValue(parsed, stripVolatileForTool(toolName))
	// json.Marshal of a map produces sorted keys in Go
	out, err := json.Marshal(normalized)
	if err != nil {
		return string(args)
	}
	return string(out)
}

// normalizeValue recursively normalizes the parsed value, stripping volatile
// fields from maps (when stripVolatile is set) and ensuring deterministic
// structure. Go's json.Marshal already sorts map keys, so we just need to
// remove volatile fields and recurse.
func normalizeValue(v interface{}, stripVolatile bool) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, vv := range val {
			if stripVolatile && volatileFields[k] {
				continue
			}
			result[k] = normalizeValue(vv, stripVolatile)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = normalizeValue(item, stripVolatile)
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

	normalized := normalizeArgs(toolName, args)
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
		// #3631: uncertain wording — normalization can miss semantics
		// (e.g. volatile fields that DO carry meaning), so never assert
		// that results are identical; only that they may be.
		return fmt.Sprintf(
			"Semantic duplicate: You called %s %d times with possibly equivalent arguments "+
				"(same parameters after normalizing key order and volatile fields). "+
				"Results may still differ if volatile fields (e.g. timestamp) carry meaning — "+
				"if you intentionally changed a parameter, keep going; otherwise, unless "+
				"context compaction has trimmed earlier results, you likely already have "+
				"this information in context.",
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
	normalized := normalizeArgs(name, args)
	return normalizedFingerprint(name, normalized)
}

func normalizedFingerprint(name, normalizedArgs string) string {
	h := sha256.Sum256([]byte(name + "|" + normalizedArgs))
	return hex.EncodeToString(h[:16])
}
