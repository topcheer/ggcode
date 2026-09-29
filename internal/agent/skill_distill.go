package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// Trajectory-to-skill distillation — inspired by CODESKILL ("Learning
// Self-Evolving Skills for Coding Agents", arXiv:2605.25430) and ACE
// (arXiv:2510.04618).
//
// ggcode already distills successful runs at two levels:
//   - playbook.go: statistical strategy hints (tool-category sequences)
//   - ratchet.go:  error-avoidance rules learned from failures
//
// What was missing is the CODESKILL delta: materializing the *procedure* of a
// successful run into a reviewable, reusable SKILL.md draft. This file writes
// a deterministic (no-LLM) draft to .ggcode/skill-drafts/ after qualifying
// runs. The user reviews it and moves it into .ggcode/skills/ to activate it
// as a first-class skill — keeping the human in the loop and the live skill
// registry free of machine-generated noise.
//
// Key design:
//   - Trigger: successful run, 3+ tool calls, and a procedure worth capturing
//     (2+ distinct commands, or a build/validate command plus file edits)
//   - Dedupe: procedure-hash in frontmatter prevents near-identical drafts
//   - Throttle: one draft per task type per calendar day
//   - Bounded: at most maxSkillDrafts drafts, oldest evicted first
//   - Multi-process safe: file lock around scan->write, atomic rename

const (
	skillDraftDirName = "skill-drafts"
	maxSkillDrafts    = 20
	maxProcedureSteps = 12
	maxDraftFiles     = 8
)

// maybeDistillSkill writes a draft skill from a completed successful run.
// Called from maybeReflect (async goroutine); never blocks or panics the loop.
func (a *Agent) maybeDistillSkill(stats *RunStats) {
	if stats == nil || !stats.Success {
		return
	}
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return
	}
	if err := distillSkillToDir(workingDir, stats, time.Now()); err != nil {
		debug.Log("skill-distill", "draft not written: %v", err)
	}
}

// distillSkillToDir is the testable core: extract the observed procedure from
// stats and persist a draft skill file under workingDir/.ggcode/skill-drafts/.
// Returns an error (without side effects) when the run does not qualify.
func distillSkillToDir(workingDir string, stats *RunStats, now time.Time) error {
	if workingDir == "" || stats == nil || !stats.Success {
		return fmt.Errorf("run not qualified: success=%v", stats != nil && stats.Success)
	}
	if stats.totalToolCalls() < 3 {
		return fmt.Errorf("run too trivial: %d tool calls", stats.totalToolCalls())
	}

	taskType := classifyTaskType(stats.UserPrompt)
	commands := normalizeProcedureSteps(stats.CommandsRun)
	files := stats.FilesEdited
	if len(files) > maxDraftFiles {
		files = files[:maxDraftFiles]
	}
	buildCmds := extractBuildCommands(stats.CommandsRun)

	// A draft needs a *procedure*: either a multi-step command sequence, or a
	// validate command plus the files it exercised. Pure chat / read-only runs
	// are not worth materializing.
	if len(commands) < 2 && !(len(buildCmds) >= 1 && len(files) >= 1) {
		return fmt.Errorf("no procedural content: %d commands, %d build cmds, %d files", len(commands), len(buildCmds), len(files))
	}

	procHash := procedureHash(taskType, commands, buildCmds)
	runID := stats.RunID()
	if len(runID) > 6 {
		runID = runID[:6]
	}

	draftDir := filepath.Join(workingDir, ".ggcode", skillDraftDirName)
	if err := os.MkdirAll(draftDir, 0755); err != nil {
		return fmt.Errorf("create draft dir: %w", err)
	}

	// Cross-process safety: same pattern as playbook Record (#2726).
	if unlock, err := util.FileLock(draftDir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("skill-distill", "failed to acquire draft lock (proceeding unlocked): %v", err)
	}

	// Dedupe: skip if an existing draft already captured this exact procedure.
	existing, err := listDraftFiles(draftDir)
	if err != nil {
		return fmt.Errorf("scan drafts: %w", err)
	}
	for _, d := range existing {
		if d.procHash == procHash {
			return fmt.Errorf("duplicate procedure (already drafted as %s)", filepath.Base(d.path))
		}
	}

	name := fmt.Sprintf("%s-%s-%s", taskType, now.Format("20060102"), runID)
	content := renderSkillDraft(taskType, name, stats, commands, files, buildCmds, procHash, now)
	path := filepath.Join(draftDir, name+".md")
	if err := atomicWriteFile(path, []byte(content)); err != nil {
		return fmt.Errorf("write draft: %w", err)
	}
	debug.Log("skill-distill", "drafted %s (hash=%s, %d steps)", filepath.Base(path), procHash, len(commands))

	// Evict oldest drafts beyond the cap.
	return evictDrafts(draftDir, existing)
}

// draftInfo summarizes an existing draft file for dedupe and eviction.
type draftInfo struct {
	path     string
	procHash string
	modTime  time.Time
}

// listDraftFiles scans the drafts dir and extracts the procedure-hash from
// each draft's frontmatter.
func listDraftFiles(dir string) ([]draftInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var drafts []draftInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		drafts = append(drafts, draftInfo{
			path:     path,
			procHash: frontmatterField(string(data), "procedure-hash"),
			modTime:  info.ModTime(),
		})
	}
	return drafts, nil
}

// evictDrafts removes the oldest drafts when the directory exceeds the cap.
func evictDrafts(dir string, existing []draftInfo) error {
	all, err := listDraftFiles(dir)
	if err != nil {
		return err
	}
	if len(all) <= maxSkillDrafts {
		return nil
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].modTime.Equal(all[j].modTime) {
			return all[i].modTime.Before(all[j].modTime)
		}
		return all[i].path < all[j].path
	})
	for _, d := range all[:len(all)-maxSkillDrafts] {
		if err := os.Remove(d.path); err != nil {
			debug.Log("skill-distill", "evict failed for %s: %v", d.path, err)
		} else {
			debug.Log("skill-distill", "evicted old draft %s", filepath.Base(d.path))
		}
	}
	_ = existing
	return nil
}

// normalizeProcedureSteps cleans the raw command list into stable procedure
// steps: strip leading "# comment" descriptions, dedupe preserving order,
// cap length.
func normalizeProcedureSteps(commands []string) []string {
	seen := make(map[string]bool, len(commands))
	var steps []string
	for _, cmd := range commands {
		cmd = stripCommandComment(cmd)
		if idx := strings.IndexByte(cmd, '\n'); idx > 0 {
			cmd = cmd[:idx]
		}
		cmd = strings.TrimSpace(cmd)
		if cmd == "" || seen[cmd] {
			continue
		}
		seen[cmd] = true
		steps = append(steps, cmd)
		if len(steps) >= maxProcedureSteps {
			break
		}
	}
	return steps
}

// procedureHash computes a stable fingerprint of the distilled procedure.
func procedureHash(taskType string, steps, buildCmds []string) string {
	h := sha256.New()
	fmt.Fprintln(h, taskType)
	for _, s := range steps {
		fmt.Fprintln(h, s)
	}
	fmt.Fprintln(h, "--build--")
	for _, s := range buildCmds {
		fmt.Fprintln(h, s)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) > 16 {
		sum = sum[:16]
	}
	return sum
}

// frontmatterField extracts "key: value" from a draft's YAML-ish frontmatter.
func frontmatterField(content, key string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	return ""
}

// renderSkillDraft produces the full draft file content with SKILL.md-style
// frontmatter so moving it into .ggcode/skills/ requires minimal editing.
func renderSkillDraft(taskType, name string, stats *RunStats, steps []string, files []string, buildCmds []string, procHash string, now time.Time) string {
	description := strings.ReplaceAll(stats.UserPrompt, "\n", " ")
	description = strings.TrimSpace(description)
	if description == "" {
		description = taskType + " procedure"
	}
	if len(description) > 120 {
		description = description[:117] + "..."
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", description)
	fmt.Fprintf(&b, "procedure-hash: %s\n", procHash)
	fmt.Fprintf(&b, "generated: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "source: ggcode auto-distill (run %s)\n", stats.RunID())
	b.WriteString("status: draft\n")
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s (draft)\n\n", name)
	b.WriteString("> Auto-distilled from a successful ggcode run. Review and edit,\n")
	b.WriteString("> then move this file into `.ggcode/skills/` to activate it as a\n")
	b.WriteString("> reusable skill.\n\n")

	b.WriteString("## Task\n\n")
	fmt.Fprintf(&b, "%s\n\n", description)

	if len(steps) > 0 {
		b.WriteString("## Observed procedure\n\n")
		for i, s := range steps {
			fmt.Fprintf(&b, "%d. `%s`\n", i+1, s)
		}
		b.WriteString("\n")
	}

	if len(files) > 0 {
		b.WriteString("## Files touched\n\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\n")
	}

	if len(buildCmds) > 0 {
		b.WriteString("## Validation commands\n\n")
		for _, c := range buildCmds {
			fmt.Fprintf(&b, "- `%s`\n", c)
		}
		b.WriteString("\n")
	}

	if len(stats.Errors) > 0 {
		b.WriteString("## Gotchas (errors hit mid-run, then recovered)\n\n")
		limit := len(stats.Errors)
		if limit > 3 {
			limit = 3
		}
		for _, e := range stats.Errors[:limit] {
			fmt.Fprintf(&b, "- %s\n", e)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Run stats\n\n%d iterations, %d tool calls, %s\n",
		stats.Iterations, stats.totalToolCalls(), formatRunDuration(stats.Duration))
	return b.String()
}

// atomicWriteFile writes via temp file + rename (crash-safe, same pattern as
// playbook.save).
func atomicWriteFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
