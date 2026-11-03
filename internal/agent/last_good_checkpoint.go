package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	checkpointMaxFiles = 10

	checkpointMinVerifyCycles = 1
)

type lastGoodCheckpoint struct {
	lastGoodFiles map[string]bool

	hasBaseline bool

	verifyCycles int

	lastVerifyFailed bool

	currentModifiedFiles map[string]bool

	filesModifiedSinceLastGood map[string]bool

	preExistingFiles map[string]bool
}

func newLastGoodCheckpoint() *lastGoodCheckpoint {
	return &lastGoodCheckpoint{
		lastGoodFiles:              make(map[string]bool),
		currentModifiedFiles:       make(map[string]bool),
		filesModifiedSinceLastGood: make(map[string]bool),
		preExistingFiles:           make(map[string]bool),
	}
}

func (c *lastGoodCheckpoint) reset() {
	if c == nil {
		return
	}
	c.lastGoodFiles = make(map[string]bool)
	c.hasBaseline = false
	c.verifyCycles = 0
	c.lastVerifyFailed = false
	c.currentModifiedFiles = make(map[string]bool)
	c.filesModifiedSinceLastGood = make(map[string]bool)
	c.preExistingFiles = make(map[string]bool)
}

func (c *lastGoodCheckpoint) recordFileEdit(filePath, workingDir string) {
	if c == nil || filePath == "" {
		return
	}
	fp := filepath.Clean(filePath)

	if _, tracked := c.preExistingFiles[fp]; !tracked {
		c.preExistingFiles[fp] = gitIsTracked(fp, workingDir)
	}
	c.currentModifiedFiles[fp] = true
	c.filesModifiedSinceLastGood[fp] = true
}

func (c *lastGoodCheckpoint) recordVerifyPass() {
	if c == nil {
		return
	}
	c.verifyCycles++
	c.hasBaseline = true
	c.lastVerifyFailed = false

	c.lastGoodFiles = make(map[string]bool, len(c.currentModifiedFiles))
	for f := range c.currentModifiedFiles {
		c.lastGoodFiles[f] = true
	}

	c.filesModifiedSinceLastGood = make(map[string]bool)

	debug.Log("last-good-checkpoint", "verify PASSED: checkpoint updated with %d files", len(c.lastGoodFiles))
}

func (c *lastGoodCheckpoint) recordVerifyFail() {
	if c == nil {
		return
	}
	c.verifyCycles++
	c.hasBaseline = true
	c.lastVerifyFailed = true
}

func (c *lastGoodCheckpoint) revertGuidance() string {
	if c == nil || !c.hasBaseline || !c.lastVerifyFailed {
		return ""
	}

	if c.verifyCycles < checkpointMinVerifyCycles {
		return ""
	}
	if len(c.filesModifiedSinceLastGood) == 0 {
		return ""
	}

	var modifiedSinceGood, newFiles []string
	for f := range c.filesModifiedSinceLastGood {
		if c.preExistingFiles[f] || c.lastGoodFiles[f] {
			modifiedSinceGood = append(modifiedSinceGood, f)
		} else {
			newFiles = append(newFiles, f)
		}
	}

	if len(modifiedSinceGood) == 0 && len(newFiles) == 0 {
		return ""
	}

	return c.formatRevertGuidance(modifiedSinceGood, newFiles)
}

func (c *lastGoodCheckpoint) formatRevertGuidance(modifiedFiles, newFiles []string) string {
	total := len(modifiedFiles) + len(newFiles)
	debug.Log("last-good-checkpoint", "generating revert guidance: %d modified, %d new since last good", len(modifiedFiles), len(newFiles))

	var b strings.Builder
	b.WriteString("[LAST-KNOWN-GOOD] The following files were modified or created after the last passing verification ")
	b.WriteString("and are the most likely source of the regressions above:\n")

	count := 0
	for _, f := range modifiedFiles {
		if count >= checkpointMaxFiles {
			b.WriteString(fmt.Sprintf("  ... and %d more file(s)\n", total-count))
			break
		}
		b.WriteString(fmt.Sprintf("  - %s\n", f))
		count++
	}
	for _, f := range newFiles {
		if count >= checkpointMaxFiles {
			b.WriteString(fmt.Sprintf("  ... and %d more file(s)\n", total-count))
			break
		}
		b.WriteString(fmt.Sprintf("  - %s (new)\n", f))
		count++
	}

	if len(modifiedFiles) > 0 {
		b.WriteString("\nTo revert to the last-known-good state:\n")
		b.WriteString("  1. Use `git diff <file>` to review what changed in each file\n")
		b.WriteString("  2. Use `git checkout -- <file>` to revert specific files\n")
		b.WriteString("  3. Or use the undo_edit tool to revert recent edits\n")
		b.WriteString("  4. After reverting, re-read the original task and try a different approach\n")
	} else {
		b.WriteString("\nThese are new files -- review and consider removing them before trying a different approach.\n")
	}
	b.WriteString("Do NOT continue making incremental fixes -- the self-correction loop is not converging.\n")

	return b.String()
}

func (a *Agent) lastGoodCheckpointRecordEdit(toolName string, fileHint string) {
	if a.lastGoodCheckpoint == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	a.mu.RLock()
	dir := a.workingDir
	a.mu.RUnlock()
	a.lastGoodCheckpoint.recordFileEdit(fileHint, dir)
}

func (a *Agent) lastGoodCheckpointRecordPass() {
	if a.lastGoodCheckpoint != nil {
		a.lastGoodCheckpoint.recordVerifyPass()
	}
}

func (a *Agent) lastGoodCheckpointRecordFail() {
	if a.lastGoodCheckpoint != nil {
		a.lastGoodCheckpoint.recordVerifyFail()
	}
}

func (a *Agent) lastGoodCheckpointGuidance() string {
	if a.lastGoodCheckpoint == nil {
		return ""
	}
	return a.lastGoodCheckpoint.revertGuidance()
}

func (a *Agent) resetLastGoodCheckpoint() {
	if a.lastGoodCheckpoint != nil {
		a.lastGoodCheckpoint.reset()
	}
}

func gitIsTracked(path, workingDir string) bool {
	if path == "" || workingDir == "" {
		return false
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workingDir, path)
	}
	return gitCommand(workingDir, "ls-files", "--error-unmatch", abs).Run() == nil
}
