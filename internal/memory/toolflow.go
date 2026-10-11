package memory

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// ToolFlow mining (r449, AWM-style workflow memory): cross-session tool-call
// sequence statistics. Where the experience store (experience.go) distills
// single cases, this finds RECURRING multi-tool workflows - the prefix→next
// transitions the user's actual usage keeps repeating - so the agent can
// treat them as established playbooks instead of re-deriving the order every
// run. Deliberately lexical/statistical (n-gram counting): no embeddings, no
// LLM, no IO beyond reading session JSONL.

// ToolFlowPattern is a high-support, high-confidence tool transition:
// after seeing Prefix (in order, within one session), Next followed in
// Confidence of the observed cases.
type ToolFlowPattern struct {
	Prefix     []string // 1 or 2 tool names
	Next       string   // the statistically dominant continuation
	Count      int      // how often Prefix→Next occurred (support)
	Confidence float64  // Count / total continuations of Prefix
}

// toolFlowMinSupport / toolFlowMinConfidence gate what counts as a pattern.
// Requiring repetition is the truth filter (same rationale as r444's
// userEditPromoteTurns=2): one-off sequences are noise, not workflow.
const (
	toolFlowMinSupport     = 5
	toolFlowMinConfidence  = 0.7
	toolFlowScanSessionCap = 50 // newest N session files

	// toolNameToken is the JSONL field marker both extractors scan for.
	toolNameToken = `"tool_name":"`
)

// ExtractToolSequence returns the ordered tool names recorded in one session
// JSONL body. Tool calls are emitted as `"tool_name":"x"` fields in call
// order; line and in-line ordering both reflect execution order.
func ExtractToolSequence(body string) []string {
	var seq []string
	for {
		i := strings.Index(body, toolNameToken)
		if i < 0 {
			return seq
		}
		rest := body[i+len(toolNameToken):]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return seq
		}
		if name := rest[:end]; name != "" {
			seq = append(seq, name)
		}
		body = rest[end:]
	}
}

// appendToolNamesFromLine is the per-line form of ExtractToolSequence
// (#3699): it pulls every `"tool_name":"..."` value out of ONE JSONL line
// so AnalyzeToolFlows can stream sessions file-by-file without ever holding
// a whole session's text in memory. The token cannot straddle a line
// boundary, so streaming yields the identical sequence to the old
// whole-body scan.
func appendToolNamesFromLine(seq []string, line []byte) []string {
	tok := []byte(toolNameToken)
	for {
		i := bytes.Index(line, tok)
		if i < 0 {
			return seq
		}
		rest := line[i+len(tok):]
		end := bytes.IndexByte(rest, '"')
		if end < 0 {
			return seq
		}
		if name := rest[:end]; len(name) > 0 {
			seq = append(seq, string(name))
		}
		line = rest[end:]
	}
}

// AnalyzeToolFlows scans the newest session files under sessionsDir and
// returns recurring tool transitions ranked by support then confidence.
// Sessions are independent sequences - transitions never span file
// boundaries, so an unrelated next-session opener cannot masquerade as a
// continuation.
func AnalyzeToolFlows(sessionsDir string, maxPatterns int) ([]ToolFlowPattern, error) {
	if maxPatterns <= 0 {
		maxPatterns = 5
	}
	matches, err := filepath.Glob(filepath.Join(sessionsDir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		fi, ei := os.Stat(matches[i])
		fj, ej := os.Stat(matches[j])
		if ei != nil || ej != nil {
			return matches[i] > matches[j]
		}
		return fi.ModTime().After(fj.ModTime())
	})
	if len(matches) > toolFlowScanSessionCap {
		matches = matches[:toolFlowScanSessionCap]
	}

	bigramCount := map[[2]string]int{}
	bigramTotal := map[string]int{}
	trigramCount := map[[3]string]int{}
	trigramTotal := map[[2]string]int{}

	scanned := 0
	for _, path := range matches {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		// #3699: stream line-by-line. The previous shape slurped the ENTIRE
		// session file into one strings.Builder before extracting tool names
		// - on a multi-hundred-MB session store (real-world long-lived
		// workspaces) each analysis pass retained the full file text in the
		// heap, which profiled as GBs of live strings.Builder allocations
		// and was the dominant term of the "agent loop eats several GB of
		// RSS" regression. The token `"tool_name":"` can never straddle a
		// JSONL line boundary, so per-line extraction is semantically
		// identical to the old whole-body scan (ExtractToolSequence).
		var seq []string
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			seq = appendToolNamesFromLine(seq, sc.Bytes())
		}
		f.Close()
		if err := sc.Err(); err != nil {
			continue
		}
		if len(seq) < 2 {
			continue
		}
		scanned++
		for i := 0; i+1 < len(seq); i++ {
			a, b := seq[i], seq[i+1]
			bigramCount[[2]string{a, b}]++
			bigramTotal[a]++
			if i+2 < len(seq) {
				c := seq[i+2]
				trigramCount[[3]string{a, b, c}]++
				trigramTotal[[2]string{a, b}]++
			}
		}
	}
	if scanned == 0 {
		return nil, nil
	}

	var pats []ToolFlowPattern
	for key, n := range trigramCount {
		total := trigramTotal[[2]string{key[0], key[1]}]
		if total == 0 {
			continue
		}
		conf := float64(n) / float64(total)
		// self-loop exclusion, see bigram loop below
		if n >= toolFlowMinSupport && conf >= toolFlowMinConfidence && key[1] != key[2] {
			pats = append(pats, ToolFlowPattern{
				Prefix: []string{key[0], key[1]}, Next: key[2],
				Count: n, Confidence: conf,
			})
		}
	}
	for key, n := range bigramCount {
		total := bigramTotal[key[0]]
		if total == 0 {
			continue
		}
		conf := float64(n) / float64(total)
		// Self-loops (X→X) are statistically real but carry no playbook
		// value - "after run_command, likely another run_command" tells the
		// agent nothing it does not know. Real-data smoke on this very
		// machine surfaced 642k run_command→run_command swamping every
		// cross-tool transition, so they are excluded outright.
		if n >= toolFlowMinSupport && conf >= toolFlowMinConfidence && key[0] != key[1] {
			pats = append(pats, ToolFlowPattern{
				Prefix: []string{key[0]}, Next: key[1],
				Count: n, Confidence: conf,
			})
		}
	}
	// Rank by support first (breadth of the habit), then confidence.
	sort.Slice(pats, func(i, j int) bool {
		if pats[i].Count != pats[j].Count {
			return pats[i].Count > pats[j].Count
		}
		if pats[i].Confidence != pats[j].Confidence {
			return pats[i].Confidence > pats[j].Confidence
		}
		return strings.Join(pats[i].Prefix, ">") < strings.Join(pats[j].Prefix, ">")
	})
	// Drop a bigram when a trigram with the same last prefix step and next
	// already carries the information at higher specificity.
	if len(pats) > maxPatterns {
		pats = pats[:maxPatterns]
	}
	debug.Log("memory", "AnalyzeToolFlows: scanned %d sessions, %d pattern(s) above support=%d conf=%.2f", scanned, len(pats), toolFlowMinSupport, toolFlowMinConfidence)
	return pats, nil
}

// FormatToolFlowPatterns renders patterns for agent consumption.
func FormatToolFlowPatterns(pats []ToolFlowPattern, generated time.Time) string {
	if len(pats) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Recurring tool workflows mined from your recent sessions (prefix → dominant continuation; use as established playbook order, not a mandate):\n")
	for _, p := range pats {
		sb.WriteString(strings.Repeat(" ", 2))
		sb.WriteString(strings.Join(append(append([]string{}, p.Prefix...), p.Next), " → "))
		sb.WriteString(" — observed ")
		sb.WriteString(strconv.Itoa(p.Count))
		sb.WriteString("x, confidence ")
		sb.WriteString(strconv.FormatFloat(p.Confidence, 'f', 2, 64))
		sb.WriteString("\n")
	}
	_ = generated
	return sb.String()
}
