package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	seqWindow = 12

	seqConsecutiveReads = 3

	seqRepeatLen   = 3
	seqRepeatCount = 3
)

type seqEntry struct {
	tool     string
	filePath string
	args     map[string]interface{}
	iter     int
}

type toolSequenceValidator struct {
	history    []seqEntry
	hintsGiven map[string]bool
}

func newToolSequenceValidator() *toolSequenceValidator {
	return &toolSequenceValidator{
		hintsGiven: make(map[string]bool),
	}
}

func (v *toolSequenceValidator) reset() {
	v.history = v.history[:0]

	v.hintsGiven = make(map[string]bool)
}

func (v *toolSequenceValidator) record(tc provider.ToolCallDelta, iter int) string {
	entry := seqEntry{
		tool: tc.Name,
		args: parseArgsMap(tc.Arguments),
		iter: iter,
	}
	entry.filePath = extractPrimaryPath(entry.tool, entry.args)

	guidance := v.checkAntiPatterns(entry)

	v.history = append(v.history, entry)
	if len(v.history) > seqWindow {
		v.history = v.history[len(v.history)-seqWindow:]
	}

	if guidance == "" {
		guidance = v.checkRepeatedSequence()
	}

	if guidance != "" {
		gLen := len(guidance)
		if gLen > 80 {
			gLen = 80
		}
		debug.Log("tool-sequence", "anti-pattern detected: %s (iter=%d tool=%s)", guidance[:gLen], iter, tc.Name)
	}

	return guidance
}

func (v *toolSequenceValidator) checkAntiPatterns(curr seqEntry) string {

	if guidance := v.checkFullReadThenTargeted(curr); guidance != "" {
		return guidance
	}

	if guidance := v.checkSequentialReads(curr); guidance != "" {
		return guidance
	}

	if guidance := v.checkDirThenGlob(curr); guidance != "" {
		return guidance
	}

	if guidance := v.checkReadThenGrep(curr); guidance != "" {
		return guidance
	}

	if guidance := v.checkBroadThenNarrowSearch(curr); guidance != "" {
		return guidance
	}

	if guidance := v.checkSymbolRetrievalDup(curr); guidance != "" {
		return guidance
	}

	return ""
}

func (v *toolSequenceValidator) checkFullReadThenTargeted(curr seqEntry) string {
	if v.hintsGiven["full_read_then_targeted"] {
		return ""
	}
	if curr.tool != "read_file" {
		return ""
	}
	if curr.filePath == "" {
		return ""
	}

	hasOffset := hasIntArg(curr.args, "offset")
	hasLimit := hasIntArg(curr.args, "limit")
	if !hasOffset && !hasLimit {
		return ""
	}

	for i := len(v.history) - 1; i >= 0; i-- {
		e := v.history[i]
		if e.tool == "read_file" && e.filePath == curr.filePath {
			if !hasIntArg(e.args, "offset") && !hasIntArg(e.args, "limit") {
				v.hintsGiven["full_read_then_targeted"] = true
				return fmt.Sprintf(
					"[tool-sequence] You previously read %s in full, now reading a subset with offset/limit. "+
						"For future reads of unfamiliar files, start with offset/limit to explore specific sections — "+
						"full reads of large files waste context budget.",
					curr.filePath,
				)
			}
		}
	}
	return ""
}

func (v *toolSequenceValidator) checkSequentialReads(curr seqEntry) string {
	if v.hintsGiven["sequential_reads"] {
		return ""
	}
	if curr.tool != "read_file" {
		return ""
	}

	consecutive := 0
	for i := len(v.history) - 1; i >= 0; i-- {
		if v.history[i].tool == "read_file" {
			consecutive++
		} else {
			break
		}
	}

	if consecutive+1 < seqConsecutiveReads {
		return ""
	}

	iters := make(map[int]bool)
	files := make(map[string]bool)
	iters[curr.iter] = true
	files[curr.filePath] = true
	for i := len(v.history) - 1; i >= len(v.history)-consecutive; i-- {
		e := v.history[i]
		iters[e.iter] = true
		if e.filePath != "" {
			files[e.filePath] = true
		}
	}
	if len(iters) < 2 {
		return ""
	}

	if len(files) < seqConsecutiveReads {
		return ""
	}
	v.hintsGiven["sequential_reads"] = true
	return fmt.Sprintf(
		"[tool-sequence] You've made %d sequential read_file calls. "+
			"Use multi_file_read to batch multiple file reads in a single call — "+
			"this saves iterations and reduces context overhead.",
		consecutive+1,
	)
}

func (v *toolSequenceValidator) checkDirThenGlob(curr seqEntry) string {
	if v.hintsGiven["dir_then_glob"] {
		return ""
	}
	if curr.tool != "glob" {
		return ""
	}
	globDir, _ := curr.args["directory"].(string)
	if globDir == "" {
		return ""
	}
	for i := len(v.history) - 1; i >= 0; i-- {
		e := v.history[i]
		if e.tool == "list_directory" && e.filePath == globDir {
			v.hintsGiven["dir_then_glob"] = true
			return fmt.Sprintf(
				"[tool-sequence] You already ran list_directory on %s. "+
					"If you need specific files, use glob or grep directly instead of "+
					"listing then searching — it saves an iteration.",
				globDir,
			)
		}
	}
	return ""
}

func (v *toolSequenceValidator) checkReadThenGrep(curr seqEntry) string {
	if v.hintsGiven["read_then_grep"] {
		return ""
	}
	if curr.tool != "grep" && curr.tool != "search_files" {
		return ""
	}
	searchPath, _ := curr.args["path"].(string)
	if searchPath == "" {
		return ""
	}
	for i := len(v.history) - 1; i >= 0; i-- {
		e := v.history[i]

		if sourceMutatingTools[e.tool] && e.filePath == searchPath {
			return ""
		}
		if e.tool == "read_file" && e.filePath == searchPath {

			if _, hasOffset := e.args["offset"]; hasOffset {
				return ""
			}
			if _, hasLimit := e.args["limit"]; hasLimit {
				return ""
			}
			v.hintsGiven["read_then_grep"] = true
			return fmt.Sprintf(
				"[tool-sequence] You already read %s in this session. "+
					"Avoid grepping a file you've already read — search within the content "+
					"you already have, or use offset/limit to re-read a specific section.",
				searchPath,
			)
		}
	}
	return ""
}

func (v *toolSequenceValidator) checkBroadThenNarrowSearch(curr seqEntry) string {
	if v.hintsGiven["broad_then_narrow"] {
		return ""
	}
	if curr.tool != "search_files" && curr.tool != "grep" {
		return ""
	}
	var currDir string
	if curr.tool == "grep" {
		currDir, _ = curr.args["path"].(string)
	} else {
		currDir, _ = curr.args["directory"].(string)
	}
	if currDir == "" {
		return ""
	}
	currPattern, _ := curr.args["pattern"].(string)
	if currPattern == "" {
		return ""
	}
	for i := len(v.history) - 1; i >= 0; i-- {
		e := v.history[i]
		if (e.tool == "search_files" || e.tool == "grep") && e.tool == curr.tool {
			var eDir string
			if e.tool == "grep" {
				eDir, _ = e.args["path"].(string)
			} else {
				eDir, _ = e.args["directory"].(string)
			}
			ePattern, _ := e.args["pattern"].(string)

			if ePattern == currPattern && eDir == "" {
				v.hintsGiven["broad_then_narrow"] = true
				return fmt.Sprintf(
					"[tool-sequence] You searched for %q broadly, then narrowed to %q. "+
						"In future searches, specify the directory upfront to get more relevant results faster.",
					currPattern, currDir,
				)
			}
		}
	}
	return ""
}

const (
	seqRetrievalGap   = 3
	seqMinSymbolRunes = 5
)

func (v *toolSequenceValidator) checkSymbolRetrievalDup(curr seqEntry) string {
	if v.hintsGiven["symbol_retrieval_dup"] {
		return ""
	}
	if curr.tool != "lsp_workspace_symbols" {
		return ""
	}
	query, _ := curr.args["query"].(string)
	if len([]rune(query)) < seqMinSymbolRunes {
		return ""
	}
	for i := len(v.history) - 1; i >= 0 && curr.iter-v.history[i].iter <= seqRetrievalGap; i-- {
		e := v.history[i]
		if e.tool != "grep" && e.tool != "search_files" {
			continue
		}
		pattern, _ := e.args["pattern"].(string)

		shorter := query
		if len(pattern) < len(query) {
			shorter = pattern
		}
		if len([]rune(shorter)) < seqMinSymbolRunes ||
			(!strings.Contains(pattern, query) && !strings.Contains(query, pattern)) {
			continue
		}

		for j := len(v.history) - 1; j > i; j-- {
			if sourceMutatingTools[v.history[j].tool] {
				return ""
			}
		}
		v.hintsGiven["symbol_retrieval_dup"] = true
		return fmt.Sprintf(
			"[tool-sequence] You recently searched for the same symbol via %s "+
				"(iteration %d); lsp_workspace_symbols on %q re-retrieves references "+
				"you likely already have. Reuse the earlier results unless they are stale.",
			e.tool, e.iter, query,
		)
	}
	return ""
}

func (v *toolSequenceValidator) checkRepeatedSequence() string {
	if v.hintsGiven["repeated_sequence"] {
		return ""
	}
	n := len(v.history)
	if n < seqRepeatLen*seqRepeatCount {
		return ""
	}

	for L := seqRepeatLen; L <= seqRepeatLen+1; L++ {
		if n < L*seqRepeatCount {
			continue
		}
		tail := v.history[n-L:]

		distinct := make(map[string]bool, len(tail))
		for _, e := range tail {
			distinct[e.tool] = true
		}
		if len(distinct) < 2 {
			continue
		}
		count := 0
		for i := 0; i+L <= n; i++ {
			match := true
			for j := 0; j < L; j++ {
				if v.history[i+j].tool != tail[j].tool {
					match = false
					break
				}

				if tail[j].filePath != "" && v.history[i+j].filePath != tail[j].filePath {
					match = false
					break
				}
			}
			if match {
				count++
			}
		}
		if count >= seqRepeatCount {
			v.hintsGiven["repeated_sequence"] = true
			tools := make([]string, 0, L)
			for _, e := range tail {
				tools = append(tools, e.tool)
			}
			return fmt.Sprintf(
				"[tool-sequence] The same tool sequence (%s) has repeated %d times this run. "+
					"This looks like a stable workflow you are re-issuing by hand - consider "+
					"saving it once (cmd_snippet save for shell portions, or a reusable skill) "+
					"and proposing it to the user instead of repeating the same steps.",
				strings.Join(tools, " -> "), count,
			)
		}
	}
	return ""
}

func parseArgsMap(args []byte) map[string]interface{} {
	if len(args) == 0 {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return nil
	}
	return m
}

func extractPrimaryPath(toolName string, args map[string]interface{}) string {
	if args == nil {
		return ""
	}

	if p, ok := args["path"].(string); ok && p != "" {
		return p
	}

	if p, ok := args["file_path"].(string); ok && p != "" {
		return p
	}
	if p, ok := args["directory"].(string); ok && p != "" {
		return p
	}
	return ""
}

func hasIntArg(args map[string]interface{}, key string) bool {
	if args == nil {
		return false
	}
	v, ok := args[key]
	if !ok {
		return false
	}
	switch n := v.(type) {
	case float64:
		return n > 0
	case int:
		return n > 0
	case json.Number:
		i, err := n.Int64()
		return err == nil && i > 0
	default:
		return false
	}
}
