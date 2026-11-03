package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	patchGiveUpThreshold = 4

	patchMaxFires = 2
)

type patchExhaustState struct {
	currentPatch         string
	consecutiveCount     int
	lastPatch            string
	lastPatchCount       int
	resumedFromExcursion bool
	patchReadCounts      map[string]int
	fires                int
}

func newPatchExhaustState() *patchExhaustState {
	return &patchExhaustState{
		patchReadCounts: make(map[string]int),
	}
}

func (p *patchExhaustState) reset() {
	p.currentPatch = ""
	p.lastPatch = ""
	p.lastPatchCount = 0
	p.resumedFromExcursion = false
	p.consecutiveCount = 0
	p.patchReadCounts = make(map[string]int)
	p.fires = 0
}

func patchOf(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\") {
		if n := weNormalizePath(path); n != "" {
			return n
		}
		return "."
	}
	return filepath.Dir(weNormalizePath(path))
}

func (p *patchExhaustState) recordRead(path string) string {
	if path == "" {
		return ""
	}
	if p.patchReadCounts == nil {
		p.patchReadCounts = make(map[string]int)
	}
	patch := patchOf(path)
	if patch == "" {
		return ""
	}
	p.patchReadCounts[patch]++

	if patch == p.currentPatch {
		p.consecutiveCount++
	} else if patch == p.lastPatch && p.lastPatchCount > 0 {

		p.consecutiveCount = p.lastPatchCount + 1
		p.lastPatch, p.lastPatchCount = "", 0
		p.resumedFromExcursion = true
	} else if p.resumedFromExcursion {

		p.lastPatch, p.lastPatchCount = "", 0
		p.resumedFromExcursion = false
		p.consecutiveCount = 1
	} else {

		p.lastPatch, p.lastPatchCount = p.currentPatch, p.consecutiveCount
		p.consecutiveCount = 1
	}
	p.currentPatch = patch

	if p.consecutiveCount >= patchGiveUpThreshold && p.fires < patchMaxFires {
		p.fires++
		n := p.consecutiveCount
		debug.Log("agent", "patch-exhaustion: %d consecutive reads into %s with no edit (IFT give-up rule)", n, patch)
		return fmt.Sprintf(
			"[Foraging hint] You have read %d files in %s without making any edits. "+
				"Information Foraging Theory calls this an exhausted patch: each further read yields diminishing new information. "+
				"Consider acting on what you have (edit/verify), or shift to a different area of the codebase where fresh information remains.",
			n, patch,
		)
	}
	return ""
}

func (p *patchExhaustState) recordEdit(path string) {
	if path == "" {
		return
	}

	p.consecutiveCount = 0
	p.currentPatch = ""
	p.lastPatch, p.lastPatchCount = "", 0
	p.resumedFromExcursion = false
}
