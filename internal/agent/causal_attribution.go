package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	causalMaxEdits = 30

	causalMaxWarnings = 3

	causalLookback = 10

	causalWtErrorFileMatch = 50
	causalWtRecency        = 10
	causalWtSameDir        = 5
	causalWtSamePackage    = 8

	causalSuspectThreshold = 25
)

type causalEditStep struct {
	iteration int
	toolName  string
	filePath  string
	dirPath   string
}

type causalAttributionState struct {
	edits    []causalEditStep
	warnings int

	lastSuspect string

	lastSuspectStep *causalEditStep
	lastSuspectCRS  int
}

func newCausalAttributionState() *causalAttributionState {
	return &causalAttributionState{
		edits: make([]causalEditStep, 0, causalMaxEdits),
	}
}

var causalEditTools = sourceMutatingTools

var causalVerifyRe = regexp.MustCompile(`(?i)(go\s+(build|test|vet)|make\s+\w+|npm\s+(test|run)|cargo\s+(build|test)|pytest|jest|\.\/gradlew)`)

var causalErrorFileRe = regexp.MustCompile("(?:^|\\s)((?:[\\w\\-./\\\\:]+)\\.(?:go|ts|tsx|js|jsx|mjs|py|rs|java|rb|kt|swift|c|cc|cpp|h|hpp))(?::\\d+:|\\(\\d+,\\d+\\):)")

func (s *causalAttributionState) recordEdit(toolName, filePath string, iteration int) {
	if !causalEditTools[toolName] {
		return
	}
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return
	}

	step := causalEditStep{
		iteration: iteration,
		toolName:  toolName,
		filePath:  filePath,
		dirPath:   dirOfFile(filePath),
	}

	if len(s.edits) >= causalMaxEdits {

		s.edits = s.edits[1:]
	}
	s.edits = append(s.edits, step)
}

func dirOfFile(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

func extractErrorFiles(output string) []string {
	matches := causalErrorFileRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var files []string
	for _, m := range matches {

		f := strings.TrimSpace(m[1])

		f = strings.ReplaceAll(f, "\\", "/")
		f = strings.TrimPrefix(f, "./")

		if strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://") {
			continue
		}
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	return files
}

func goPackageOf(path string) string {
	if !strings.HasSuffix(path, ".go") {
		return ""
	}
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

func normalizeCausalPath(p string) string {
	return filepath.ToSlash(filepath.Clean(p))
}

func computeCRS(edit causalEditStep, errorFiles []string, recencyRank int) int {
	score, _, _ := computeCRSDetail(edit, errorFiles, recencyRank, nil)
	return score
}

func computeCRSDetail(edit causalEditStep, errorFiles []string, recencyRank int, bareAmbiguous map[string]bool) (int, bool, bool) {
	score := 0
	fileMatch := false

	evidenceHit := false
	dirMatch := false

	for _, ef := range errorFiles {
		efN := normalizeCausalPath(ef)
		editN := normalizeCausalPath(edit.filePath)

		if efN == editN {
			score += causalWtErrorFileMatch
			fileMatch = true
			evidenceHit = true
			continue
		}

		suffixTier := func(long, short string) (hit, strong bool) {
			if strings.Contains(short, "/") {

				return strings.HasSuffix(long, "/"+short), !bareAmbiguous[short]
			}
			if filepath.Base(long) == short {
				return true, !bareAmbiguous[short]
			}
			return false, false
		}

		suffixTier2 := func(long, short, ambiguityAnchor string) (hit, strong bool) {
			if strings.Contains(short, "/") {
				return strings.HasSuffix(long, "/"+short), !bareAmbiguous[ambiguityAnchor]
			}
			if filepath.Base(long) == short {
				return true, !bareAmbiguous[ambiguityAnchor]
			}
			return false, false
		}
		if hit, strong := suffixTier(editN, efN); hit {
			if strong {
				score += causalWtErrorFileMatch
				fileMatch = true
			} else {
				score += causalWtSameDir
				dirMatch = true
			}
			evidenceHit = true
			continue
		}
		if hit, strong := suffixTier2(efN, editN, efN); hit {
			if strong {
				score += causalWtErrorFileMatch
				fileMatch = true
			} else {
				score += causalWtSameDir
				dirMatch = true
			}
			evidenceHit = true
			continue
		}

		ep := goPackageOf(efN)
		tp := goPackageOf(editN)
		if ep != "" && tp != "" && ep == tp {
			score += causalWtSamePackage
			evidenceHit = true
			dirMatch = true
			continue
		}

		ed := dirOfFile(efN)
		if ed != "" && ed == normalizeCausalPath(edit.dirPath) {
			score += causalWtSameDir
			evidenceHit = true
			dirMatch = true
		}
	}

	if evidenceHit {
		score += recencyRank * causalWtRecency
	}

	return score, fileMatch, dirMatch
}

func bareAmbiguousNames(edits []causalEditStep, errorFiles []string) map[string]bool {
	ambiguous := map[string]bool{}
	for _, ef := range errorFiles {
		efN := normalizeCausalPath(ef)
		if efN == "" {
			continue
		}
		seen := map[string]bool{}
		for _, e := range edits {
			editN := normalizeCausalPath(e.filePath)
			var hit bool
			if strings.Contains(efN, "/") {

				hit = strings.HasSuffix(editN, "/"+efN)
			} else {
				hit = filepath.Base(editN) == efN
			}
			if hit {
				seen[editN] = true
			}
		}
		if len(seen) >= 2 {
			ambiguous[efN] = true
		}
	}
	return ambiguous
}

var readCmdPrefixes = []string{"grep", "rg", "cat ", "head", "tail", "awk", "sed -n", "find ", "less", "bat "}

func causalCmdForGate(tc provider.ToolCallDelta, content string) string {
	if cmd := extractStringField(tc.Arguments, "command"); cmd != "" {
		return cmd
	}
	if tc.Name == "wait_command" || tc.Name == "read_command_output" {
		var lines []string
		inBlock := false
		for _, ln := range strings.Split(content, "\n") {
			if !inBlock {
				if v, ok := strings.CutPrefix(ln, "Command: "); ok {
					inBlock = true
					lines = append(lines, strings.TrimSpace(v))
				}
				continue
			}
			if v, ok := strings.CutPrefix(ln, "    "); ok {
				lines = append(lines, v)
				continue
			}
			break
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func looksLikeReadCommand(cmd string) bool {

	if strings.Contains(cmd, "\n") {
		for _, ln := range strings.Split(cmd, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			if looksLikeReadCommand(ln) {
				return true
			}
		}
		return false
	}
	c := strings.TrimSpace(cmd)
	for _, p := range readCmdPrefixes {
		if strings.HasPrefix(c, p) || strings.HasPrefix(c, "./"+p) {
			return true
		}
	}

	if i := strings.LastIndex(c, "&&"); i >= 0 {
		return looksLikeReadCommand(c[i+2:])
	}
	if i := strings.LastIndex(c, "|"); i >= 0 {
		return looksLikeReadCommand(c[i+1:])
	}
	return false
}

func (s *causalAttributionState) attributeFailureCmd(output, cmd string, errored bool) string {
	if !errored && looksLikeReadCommand(cmd) {
		return ""
	}
	return s.attributeFailure(output)
}

func (s *causalAttributionState) attributeFailure(output string) string {
	if s.warnings >= causalMaxWarnings {
		return ""
	}
	if len(s.edits) == 0 {
		return ""
	}

	looksVerify := causalVerifyRe.MatchString(output)
	looksFail := looksLikeFailure(output)

	looksLikeCmdOutput := strings.Contains(output, "exit status") ||
		strings.Contains(output, "exit code") ||
		strings.Contains(output, "FAIL") ||
		strings.Contains(output, "cannot use") ||
		strings.Contains(output, "undefined:") ||
		strings.Contains(output, "build failed") ||
		strings.Contains(output, "vet:")
	if !looksFail || (!looksVerify && !looksLikeCmdOutput) {
		return ""
	}

	errorFiles := extractErrorFiles(output)

	start := len(s.edits) - causalLookback
	if start < 0 {
		start = 0
	}
	recent := s.edits[start:]

	type scored struct {
		step      causalEditStep
		score     int
		rank      int
		fileMatch bool
		dirMatch  bool
	}

	var results []scored
	for i, edit := range recent {

		recencyRank := i + 1
		score, matched, dirHit := computeCRSDetail(edit, errorFiles, recencyRank, bareAmbiguousNames(s.edits, errorFiles))
		results = append(results, scored{step: edit, score: score, rank: i, fileMatch: matched, dirMatch: dirHit})
	}

	if len(results) == 0 {
		return ""
	}

	best := results[0]
	for _, r := range results[1:] {
		if r.score > best.score || (r.score == best.score && r.rank > best.rank) {
			best = r
		}
	}

	if best.score < causalSuspectThreshold {
		return ""
	}

	s.warnings++
	s.lastSuspect = best.step.filePath
	stepCopy := best.step
	s.lastSuspectStep = &stepCopy
	s.lastSuspectCRS = best.score

	var sb strings.Builder
	sb.WriteString("[causal-attribution] ")
	if best.fileMatch {
		sb.WriteString(fmt.Sprintf("Build/test failure likely caused by your %s to %s (step %d, CRS=%d — error output references this file). ",
			best.step.toolName, best.step.filePath, best.step.iteration, best.score))
	} else if best.dirMatch {
		sb.WriteString(fmt.Sprintf("Build/test failure most likely originated from your %s to %s (step %d, CRS=%d — same package/directory as error). ",
			best.step.toolName, best.step.filePath, best.step.iteration, best.score))
	} else {
		sb.WriteString(fmt.Sprintf("Build/test failure most likely originated from your %s to %s (step %d, CRS=%d — error-file overlap). ",
			best.step.toolName, best.step.filePath, best.step.iteration, best.score))
	}
	sb.WriteString("Review that change first before attempting a blind fix. ")
	sb.WriteString("Consider: git diff that file, check if the error line maps to your edit, or revert and re-verify.")

	return sb.String()
}

func looksLikeFailure(output string) bool {
	outputLower := strings.ToLower(output)
	failPatterns := []string{
		"fail", "error:", "panic:", "undefined:", "cannot find",
		"compile error", "compilation error", "build failed", "test failed",
		"fatal error", "syntax error", "type mismatch",
	}
	for _, p := range failPatterns {
		if strings.Contains(outputLower, p) {
			return true
		}
	}
	return false
}

func (s *causalAttributionState) reset() {
	s.edits = s.edits[:0]
	s.warnings = 0
	s.lastSuspectStep = nil
	s.lastSuspectCRS = 0
}

func (s *causalAttributionState) takeFinalSuspect() (*causalEditStep, int) {
	step := s.lastSuspectStep
	s.lastSuspectStep = nil
	crs := s.lastSuspectCRS
	s.lastSuspectCRS = 0
	return step, crs
}
