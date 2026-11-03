package agent

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	cascadeSoftThreshold = 3

	cascadeHardThreshold = 4

	cascadeAbortThreshold = 5

	cascadeMaxRoots = 20
)

type cascadeRootType int

const (
	cascadeRootNone cascadeRootType = iota
	cascadeRootFile
	cascadeRootSymbol
)

func (r cascadeRootType) String() string {
	switch r {
	case cascadeRootFile:
		return "file"
	case cascadeRootSymbol:
		return "symbol"
	default:
		return "unknown"
	}
}

type cascadeEntry struct {
	toolName string
	rootKey  string
	rootType cascadeRootType
}

type errorCascadeState struct {
	mu sync.Mutex

	roots map[string][]cascadeEntry

	firedTier map[string]int

	totalErrors int
}

func newErrorCascadeState() *errorCascadeState {
	return &errorCascadeState{
		roots:     make(map[string][]cascadeEntry),
		firedTier: make(map[string]int),
	}
}

func (e *errorCascadeState) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.roots = make(map[string][]cascadeEntry)
	e.firedTier = make(map[string]int)
	e.totalErrors = 0
}

var filePathRe = regexp.MustCompile(`(?:/[a-zA-Z0-9._-]+)+\.[a-zA-Z]+|[a-zA-Z]:\\(?:[a-zA-Z0-9._-]+\\)+[a-zA-Z0-9._-]+\.[a-zA-Z]+`)

var quotedFileRe = regexp.MustCompile(`["']([^"']+\.[a-zA-Z]+)["']`)

var symbolRe = regexp.MustCompile(`(?:undefined|undeclared|cannot find|not defined|no such|unknown)[:\s]+([a-zA-Z_][a-zA-Z0-9_.]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)?)`)

func extractCascadeRoot(toolName, content string) (string, cascadeRootType) {
	if content == "" {
		return "", cascadeRootNone
	}

	if isEditingTool(toolName) {

	}

	if matches := quotedFileRe.FindStringSubmatch(content); len(matches) > 1 {
		path := normalizeCascadePath(matches[1])
		if path != "" {
			return path, cascadeRootFile
		}
	}

	if raw := filePathRe.FindString(content); raw != "" {
		path := normalizeCascadePath(raw)
		if path != "" {
			return path, cascadeRootFile
		}
	}

	if matches := symbolRe.FindStringSubmatch(content); len(matches) > 1 {
		sym := strings.TrimSpace(matches[1])
		symLower := strings.ToLower(sym)

		if len(sym) >= 3 && !isGoKeyword(sym) && !cascadeSymbolStopList[symLower] {
			return symLower, cascadeRootSymbol
		}
	}

	return "", cascadeRootNone
}

func normalizeCascadePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "/")

	var parts []string
	for _, p := range strings.Split(path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return ""
	}

	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return parts[0]
}

func isEditingTool(name string) bool {
	return sourceMutatingTools[name]
}

func isGoKeyword(s string) bool {
	keywords := map[string]bool{
		"func": true, "var": true, "const": true, "type": true,
		"struct": true, "interface": true, "package": true,
		"import": true, "return": true, "break": true, "continue": true,
		"true": true, "false": true, "nil": true, "range": true,
		"for": true, "if": true, "else": true, "switch": true,
		"case": true, "default": true, "defer": true, "go": true,
	}
	return keywords[strings.ToLower(s)]
}

var cascadeSymbolStopList = map[string]bool{
	"file": true, "files": true, "directory": true, "directories": true,
	"command": true, "flag": true, "option": true, "module": true,
	"package": true, "task": true, "tool": true, "user": true,
	"host": true, "name": true, "path": true, "symbol": true, "type": true,
}

func (e *errorCascadeState) recordError(toolName, content string) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.totalErrors++

	rootKey, rootType := extractCascadeRoot(toolName, content)
	if rootKey == "" {
		return ""
	}

	entry := cascadeEntry{
		toolName: toolName,
		rootKey:  rootKey,
		rootType: rootType,
	}

	e.roots[rootKey] = append(e.roots[rootKey], entry)

	if len(e.roots) > cascadeMaxRoots {
		e.evictMinRoot(rootKey)
	}

	count := len(e.roots[rootKey])

	var tier int
	switch {
	case count >= cascadeAbortThreshold:
		tier = 3
	case count >= cascadeHardThreshold:
		tier = 2
	case count >= cascadeSoftThreshold:
		tier = 1
	}
	if tier <= e.firedTier[rootKey] {
		return ""
	}

	var guidance string
	switch tier {
	case 3:
		e.firedTier[rootKey] = tier
		guidance = fmt.Sprintf(
			"[Error Cascade: ABORT] %d tool failures share root cause %s %q. "+
				"The current approach is not working -- every operation touching this "+
				"%s fails. STOP attempting operations on %q. Instead: (1) re-read "+
				"the %s from scratch to understand its current state, (2) check if "+
				"another process or agent modified it, (3) consider reverting to a "+
				"known-good state, or (4) escalate to the user if this is an "+
				"environment issue.",
			count, rootType, rootKey, rootType, rootKey, rootType,
		)
	case 2:
		e.firedTier[rootKey] = tier
		guidance = fmt.Sprintf(
			"[Error Cascade: ROOT CAUSE] %d tool failures share root cause %s %q. "+
				"These are NOT independent errors -- they all stem from the same "+
				"underlying problem with this %s. FIX %q first before attempting "+
				"any other dependent operations. Common root causes: syntax error, "+
				"missing import, incorrect type, renamed symbol, or file corruption.",
			count, rootType, rootKey, rootType, rootKey,
		)
	case 1:
		e.firedTier[rootKey] = tier
		guidance = fmt.Sprintf(
			"[Error Cascade] %d tool failures share root cause %s %q. "+
				"Multiple errors are clustering around this %s -- they likely share "+
				"a common root cause. Focus on fixing %q first; fixing it may "+
				"resolve several downstream errors at once.",
			count, rootType, rootKey, rootType, rootKey,
		)
	}

	if guidance != "" {
		debug.Log("cascade", "cascade detected: root=%s type=%s count=%d tool=%s",
			rootKey, rootType, count, toolName)
	}

	return guidance
}

func (e *errorCascadeState) cascadeStats() (totalRoots, maxCluster, totalErrors int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	maxCluster = 0
	for _, entries := range e.roots {
		if len(entries) > maxCluster {
			maxCluster = len(entries)
		}
	}
	return len(e.roots), maxCluster, e.totalErrors
}

func (e *errorCascadeState) evictMinRoot(rootKey string) {
	var minKey, secondMinKey string
	minCount, secondMinCount := -1, -1
	for k, v := range e.roots {
		c := len(v)
		if minCount == -1 || c < minCount {
			secondMinKey, secondMinCount = minKey, minCount
			minKey, minCount = k, c
		} else if secondMinCount == -1 || c < secondMinCount {
			secondMinKey, secondMinCount = k, c
		}
	}
	evictKey := minKey
	if minKey == rootKey && secondMinKey != "" {
		evictKey = secondMinKey
	}
	if evictKey != "" && evictKey != rootKey {
		delete(e.roots, evictKey)

		delete(e.firedTier, evictKey)
	}
}
