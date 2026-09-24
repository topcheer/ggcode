package runeval

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/provider"
)

// Grounding axis: deterministic, judge-free verification that the file
// paths cited in the final answer actually appear in this run's evidence
// trace. Inspired by GroundEval (arXiv:2606.22737, 2026): frontier LLM
// judges scored a plausible agent response 0.85+ even though the trace
// showed the agent never retrieved the artifact the answer depended on.
// A set-membership check over the recorded trajectory detects that
// failure class with zero model calls: a cited path is "grounded" when
// the run's own tool calls opened/edited/wrote it, or when some tool
// result surfaced it; anything else is an ungrounded citation — a
// plausible answer resting on an evidence path that does not exist in
// the trace.
//
// This is an evaluation-side signal only: runeval never steers the live
// loop (it is not a detector). It runs purely inside the user-invoked
// /runreport scorecard.

const (
	// ungroundedListCap bounds how many ungrounded paths are listed in
	// the report (the counts stay exact).
	ungroundedListCap = 6

	// maxCitedPaths bounds extraction work on pathological answers.
	maxCitedPaths = 64

	// answerTextCap bounds the slice of the final assistant text scanned.
	answerTextCap = 64 << 10

	// outputScanBudget bounds the total tool-result bytes scanned per
	// cited path for the lenient "surfaced in a result" check. The walk
	// order is fixed, so results stay deterministic.
	outputScanBudget = 4 << 20

	// maxPathTokenLen rejects absurdly long tokens.
	maxPathTokenLen = 200
)

// fileToolPathFields maps file-touching tools to the JSON input fields
// that carry their paths. Plain strings and arrays of {path: ...}
// objects (multi_file_read) are both accepted.
var fileToolPathFields = map[string][]string{
	"read_file":       {"path"},
	"write_file":      {"path"},
	"edit_file":       {"file_path", "path"},
	"multi_edit_file": {"file_path", "path"},
	"notebook_edit":   {"notebook_path"},
	"multi_file_read": {"files"},
}

var (
	urlRe     = regexp.MustCompile(`(?i)\b(?:https?|ftp|file)://\S+`)
	pathRe    = regexp.MustCompile(`[A-Za-z0-9_.\-/]+\.[A-Za-z0-9]+`)
	lineRefRe = regexp.MustCompile(`[:#]L?[0-9]+$`)
)

// knownExt decides whether a bare (slash-less) dotted token counts as a
// path reference. Slash-bearing tokens are always considered paths.
var knownExt = map[string]bool{
	"go": true, "ts": true, "tsx": true, "js": true, "jsx": true,
	"mjs": true, "cjs": true, "py": true, "pyi": true, "rs": true,
	"java": true, "kt": true, "kts": true, "rb": true, "c": true,
	"h": true, "cc": true, "cpp": true, "hpp": true, "hh": true,
	"hxx": true, "cs": true, "fs": true, "fsx": true, "vb": true,
	"swift": true, "m": true, "mm": true, "dart": true, "lua": true,
	"pl": true, "php": true, "vue": true, "svelte": true, "astro": true,
	"html": true, "htm": true, "css": true, "scss": true, "sass": true,
	"less": true, "json": true, "json5": true, "jsonc": true,
	"yaml": true, "yml": true, "toml": true, "tml": true, "ini": true,
	"cfg": true, "conf": true, "env": true, "properties": true,
	"xml": true, "csv": true, "tsv": true, "md": true, "markdown": true,
	"rst": true, "adoc": true, "txt": true, "log": true, "sql": true,
	"ddl": true, "proto": true, "graphql": true, "gql": true,
	"tf": true, "tfvars": true, "hcl": true, "dockerfile": true,
	"makefile": true, "mk": true, "cmake": true, "gradle": true,
	"sh": true, "bash": true, "zsh": true, "fish": true, "ps1": true,
	"psm1": true, "bat": true, "cmd": true, "awk": true, "sed": true,
	"mod": true, "sum": true, "lock": true, "ipynb": true,
	"ggskill": true, "gemspec": true, "rake": true, "cabal": true,
	"hs": true, "elm": true, "ex": true, "exs": true, "erl": true,
	"hrl": true, "clj": true, "cljs": true, "cljc": true, "edn": true,
	"scala": true, "sc": true, "groovy": true,
}

// applyGrounding fills the grounding-axis fields of the report. It runs
// only when the trajectory actually used tools — with no tool trace
// there is no evidence to compare against and flagging citations would
// be noise.
func applyGrounding(r *Report, msgs []provider.Message) {
	if r.ToolCalls <= 0 {
		return
	}
	answer := lastAssistantText(msgs)
	if answer == "" {
		return
	}
	touched := touchedFilePaths(msgs)
	budget := outputScanBudget
	for _, p := range citedPathsFromText(answer) {
		if r.CitedPaths >= maxCitedPaths {
			break
		}
		r.CitedPaths++
		if groundedPath(p, touched) || surfacedInResults(msgs, p, &budget) {
			continue
		}
		r.UngroundedCount++
		if len(r.UngroundedCitations) < ungroundedListCap {
			r.UngroundedCitations = append(r.UngroundedCitations, p)
		}
	}
}

// groundedPath reports whether p is in the touched set, either directly
// or as a path-relative suffix of a touched absolute path (answers
// usually cite repo-relative paths while tool inputs carry absolutes).
func groundedPath(p string, touched map[string]bool) bool {
	if touched[p] {
		return true
	}
	if !strings.Contains(p, "/") {
		return false
	}
	for t := range touched {
		if strings.HasSuffix(t, "/"+p) {
			return true
		}
	}
	return false
}

// touchedFilePaths walks the log and returns the normalized set of file
// paths the agent's own tool calls opened, edited, wrote, or created.
func touchedFilePaths(msgs []provider.Message) map[string]bool {
	touched := map[string]bool{}
	for i := range msgs {
		for _, b := range msgs[i].Content {
			if b.Type != "tool_use" {
				continue
			}
			for _, p := range inputPaths(b.ToolName, b.Input) {
				if n := normalizeCitedPath(p); n != "" {
					touched[n] = true
				}
			}
		}
	}
	return touched
}

// inputPaths extracts path strings from one tool_use input JSON.
func inputPaths(tool string, raw json.RawMessage) []string {
	fields, ok := fileToolPathFields[tool]
	if !ok || len(raw) == 0 {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var out []string
	for _, f := range fields {
		switch val := v[f].(type) {
		case string:
			out = append(out, val)
		case []any:
			for _, e := range val {
				switch ev := e.(type) {
				case string:
					out = append(out, ev)
				case map[string]any:
					if p, ok := ev["path"].(string); ok {
						out = append(out, p)
					}
				}
			}
		}
	}
	return out
}

// citedPathsFromText extracts normalized, deduplicated path tokens from
// the final answer text. URLs are stripped first so their path segments
// are not misread as file references.
func citedPathsFromText(text string) []string {
	if text == "" {
		return nil
	}
	if len(text) > answerTextCap {
		text = text[len(text)-answerTextCap:]
	}
	clean := urlRe.ReplaceAllString(text, "")
	seen := map[string]bool{}
	var out []string
	for _, m := range pathRe.FindAllString(clean, -1) {
		p := normalizeCitedPath(m)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// normalizeCitedPath cleans a raw path token: line/column refs, trailing
// punctuation, "./" prefixes. Returns "" when the token is not a
// credible path reference (version numbers, bare config keys, ...).
func normalizeCitedPath(tok string) string {
	if tok == "" || len(tok) > maxPathTokenLen {
		return ""
	}
	t := lineRefRe.ReplaceAllString(tok, "")
	for len(t) > 0 && strings.ContainsRune(".,;:)]}>\"'`", rune(t[len(t)-1])) {
		t = t[:len(t)-1]
	}
	for strings.HasPrefix(t, "./") {
		t = t[2:]
	}
	if t == "" || t == "." || t == ".." {
		return ""
	}
	slash := strings.LastIndexByte(t, '/')
	stem := t
	if slash >= 0 {
		stem = t[slash+1:]
	}
	dot := strings.LastIndexByte(stem, '.')
	if dot < 0 || dot == len(stem)-1 {
		return ""
	}
	ext := strings.ToLower(stem[dot+1:])
	if !hasLetter(ext) {
		return "" // version-like: 1.2.3, v1.2
	}
	if slash < 0 && !knownExt[ext] {
		return "" // bare token with unknown ext: config.value, a.b
	}
	return t
}

// surfacedInResults reports whether path appears in any tool_result
// output, consuming a shared byte budget to bound scan cost.
func surfacedInResults(msgs []provider.Message, path string, budget *int) bool {
	if *budget <= 0 {
		return false
	}
	for i := range msgs {
		for _, b := range msgs[i].Content {
			if b.Type != "tool_result" {
				continue
			}
			out := b.Output
			if len(out) > *budget {
				out = out[:*budget]
			}
			*budget -= len(out)
			if strings.Contains(out, path) {
				return true
			}
			if *budget <= 0 {
				return false
			}
		}
	}
	return false
}

// lastAssistantText returns the text of the last assistant message that
// carries textual content. If the run ended on a tool turn, this is the
// agent's latest narration rather than a true final answer; acceptable
// for a report heuristic.
func lastAssistantText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		for _, b := range msgs[i].Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return b.Text
			}
		}
	}
	return ""
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
