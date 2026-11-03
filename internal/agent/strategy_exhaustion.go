package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	seMaxDistinctStrategies = 4

	seMaxWarnings = 2

	seFingerprintMaxLen = 500

	seToolHistoryWindow = 30
)

type seStrategyExhaustionState struct {
	mu sync.Mutex

	toolHistory []string

	errorClusters map[string]*seErrorCluster

	warningCount int

	lastWarnIteration int
}

type seErrorCluster struct {
	fingerprintsSeen int

	strategySignatures map[string]bool

	toolsSinceLastError []string
}

func newStrategyExhaustionState() *seStrategyExhaustionState {
	return &seStrategyExhaustionState{
		toolHistory:       make([]string, 0, seToolHistoryWindow),
		errorClusters:     make(map[string]*seErrorCluster),
		lastWarnIteration: -1,
	}
}

func (s *seStrategyExhaustionState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolHistory = s.toolHistory[:0]
	s.errorClusters = make(map[string]*seErrorCluster)
	s.warningCount = 0
	s.lastWarnIteration = -1
}

func (s *seStrategyExhaustionState) recordToolCall(toolName string, isError bool, errorContent string, iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.toolHistory = append(s.toolHistory, toolName)
	if len(s.toolHistory) > seToolHistoryWindow {
		s.toolHistory = s.toolHistory[len(s.toolHistory)-seToolHistoryWindow:]
	}

	if !isError {

		for _, cluster := range s.errorClusters {
			if cluster.fingerprintsSeen > 0 {
				cluster.toolsSinceLastError = append(cluster.toolsSinceLastError, toolName)
			}
		}
		return ""
	}

	fp := seFingerprintError(errorContent)

	cl, exists := s.errorClusters[fp]
	if !exists {
		cl = &seErrorCluster{
			strategySignatures: make(map[string]bool),
		}
		s.errorClusters[fp] = cl
	}

	if cl.fingerprintsSeen > 0 && len(cl.toolsSinceLastError) > 0 {
		sig := seStrategySignature(cl.toolsSinceLastError)
		if sig != seStrategySignature(nil) && !cl.strategySignatures[sig] {
			cl.strategySignatures[sig] = true
			debug.Log("agent", "strategy exhaustion: new distinct strategy #%d for error %s",
				len(cl.strategySignatures), fp[:seMin(16, len(fp))])
		}
	}

	cl.fingerprintsSeen++
	cl.toolsSinceLastError = cl.toolsSinceLastError[:0]

	if len(cl.strategySignatures) >= seMaxDistinctStrategies &&
		cl.fingerprintsSeen >= seMaxDistinctStrategies+1 {
		if s.warningCount < seMaxWarnings && s.lastWarnIteration != iteration {
			s.warningCount++
			s.lastWarnIteration = iteration
			return seBuildWarning(len(cl.strategySignatures), cl.fingerprintsSeen)
		}
	}

	return ""
}

var (
	seLineNumRe  = regexp.MustCompile(`:\d+:\d*`)
	seHexRe      = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	seHashRe     = regexp.MustCompile(`\b[0-9a-fA-F]{12,}\b`)
	seNumRe      = regexp.MustCompile(`\b\d+\b`)
	seStackRe    = regexp.MustCompile(`\(goroutine \d+.*?\)`)
	seFilePathRe = regexp.MustCompile(`/[^\s:]+\.go`)
	seWSRe       = regexp.MustCompile(`\s+`)
)

func seFingerprintError(content string) string {
	c := content
	if len(c) > seFingerprintMaxLen {
		c = c[:seFingerprintMaxLen]
	}

	c = seFilePathRe.ReplaceAllString(c, "<path>")
	c = seLineNumRe.ReplaceAllString(c, ":<ln>")
	c = seHexRe.ReplaceAllString(c, "0x<addr>")
	c = seHashRe.ReplaceAllString(c, "<hash>")
	c = seNumRe.ReplaceAllString(c, "<n>")
	c = seStackRe.ReplaceAllString(c, "")
	c = seWSRe.ReplaceAllString(c, " ")
	c = strings.TrimSpace(c)

	h := sha256.Sum256([]byte(c))
	return hex.EncodeToString(h[:8])
}

var seRecoveryIrrelevantTools = map[string]bool{
	"read_file": true, "grep": true, "glob": true, "search_files": true,
	"list_directory": true, "code_search": true, "lsp_hover": true,
	"lsp_symbols": true, "lsp_definition": true, "lsp_references": true,
	"lsp_diagnostics": true, "web_search": true,
}

func seStrategySignature(tools []string) string {
	set := make(map[string]bool, len(tools))
	for _, t := range tools {
		if seRecoveryIrrelevantTools[t] {
			continue
		}
		set[t] = true
	}
	names := make([]string, 0, len(set))
	for t := range set {
		names = append(names, t)
	}
	sort.Strings(names)
	joined := strings.Join(names, "|")
	h := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(h[:8])
}

func seBuildWarning(distinctStrategies, totalOccurrences int) string {
	return fmt.Sprintf(
		"[Strategy Exhaustion] You've tried %d distinct recovery strategies "+
			"for an error that has recurred %d times - none resolved it. "+
			"Continuing to try variations is unlikely to succeed. Consider: "+
			"(1) decompose the problem into smaller subtasks and verify each "+
			"independently, (2) step back and re-examine the root cause from a "+
			"fundamentally different angle, or (3) ask the user for guidance. "+
			"(Research: EEA robustness entropy, MiRA subgoal decomposition)",
		distinctStrategies, totalOccurrences)
}

func seMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
