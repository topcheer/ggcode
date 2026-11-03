package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	equivWarnThreshold = 3
	equivMaxWarnings   = 2
)

var volatileFields = map[string]bool{
	"trace_id":       true,
	"request_id":     true,
	"timestamp":      true,
	"_t":             true,
	"nonce":          true,
	"correlation_id": true,
}

type toolEquivDetectState struct {
	normalizedCounts map[string]int
	toolNames        map[string]string
	rawSeen          map[string]bool
	rawCount         map[string]int
	escalated        map[string]bool
	softFired        map[string]bool
	warnings         int
}

func newToolEquivDetectState() *toolEquivDetectState {
	return &toolEquivDetectState{
		normalizedCounts: make(map[string]int),
		toolNames:        make(map[string]string),
		rawSeen:          make(map[string]bool),
		rawCount:         make(map[string]int),
		escalated:        make(map[string]bool),
		softFired:        make(map[string]bool),
	}
}

func (s *toolEquivDetectState) reset() {
	s.normalizedCounts = make(map[string]int)
	s.toolNames = make(map[string]string)
	s.rawSeen = make(map[string]bool)
	s.rawCount = make(map[string]int)
	s.escalated = make(map[string]bool)
	s.softFired = make(map[string]bool)
	s.warnings = 0
}

const mcpToolNamePrefix = "mcp__"

func stripVolatileForTool(toolName string) bool {
	return !strings.HasPrefix(toolName, mcpToolNamePrefix)
}

func normalizeArgs(toolName string, args []byte) string {
	if len(args) == 0 {
		return ""
	}
	var parsed interface{}
	if err := json.Unmarshal(args, &parsed); err != nil {

		return string(args)
	}
	normalized := normalizeValue(parsed, stripVolatileForTool(toolName))

	out, err := json.Marshal(normalized)
	if err != nil {
		return string(args)
	}
	return string(out)
}

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

func (s *toolEquivDetectState) recordCall(toolName string, args []byte, rawFp string) string {
	if s.warnings >= equivMaxWarnings {
		return ""
	}

	normalized := normalizeArgs(toolName, args)
	normFp := normalizedFingerprint(toolName, normalized)

	s.normalizedCounts[normFp]++
	s.toolNames[normFp] = toolName

	rawSameFp := s.rawCount[normFp+"|"+rawFp] + 1
	s.rawCount[normFp+"|"+rawFp] = rawSameFp
	if rawSameFp == s.normalizedCounts[normFp] {
		s.rawSeen[normFp] = true
	} else {
		s.rawSeen[normFp] = false
	}

	count := s.normalizedCounts[normFp]

	if count >= equivWarnThreshold && !s.rawSeen[normFp] && !s.softFired[normFp] {
		s.softFired[normFp] = true
		s.warnings++

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

	if count >= equivWarnThreshold*3 && !s.rawSeen[normFp] && !s.escalated[normFp] {
		s.escalated[normFp] = true
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

func fingerprintToolCallNormalized(name string, args []byte) string {
	normalized := normalizeArgs(name, args)
	return normalizedFingerprint(name, normalized)
}

func normalizedFingerprint(name, normalizedArgs string) string {
	h := sha256.Sum256([]byte(name + "|" + normalizedArgs))
	return hex.EncodeToString(h[:16])
}
