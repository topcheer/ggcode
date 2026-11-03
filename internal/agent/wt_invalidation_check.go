package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	maxWTInvalidationWarnings = 2

	minReadsBeforeWarning = 2
)

type wtInvalidationState struct {
	readFiles map[string]bool

	warnedCount int

	readsSinceWarning int
}

func newWTInvalidationState() *wtInvalidationState {
	return &wtInvalidationState{
		readFiles: make(map[string]bool),
	}
}

func (w *wtInvalidationState) reset() {
	w.readFiles = make(map[string]bool)
	w.warnedCount = 0
	w.readsSinceWarning = 0
}

func (w *wtInvalidationState) recordRead(path string) {
	normalized := normalizeWTPath(path)
	if normalized == "" {
		return
	}
	w.readFiles[normalized] = true
	w.readsSinceWarning++
}

func isWTMutatingTool(toolName string) bool {
	switch toolName {
	case "git_checkout", "git_reset", "git_stash", "git_pull",
		"git_revert", "git_merge", "git_rebase", "git_cherry_pick":
		return true
	default:
		return false
	}
}

var readOnlyGitSubcommands = map[string]bool{
	"status": true,
	"list":   true,
	"diff":   true,
	"log":    true,
	"show":   true,
	"branch": true,
}

var readOnlyStashActions = map[string]bool{
	"list": true,
	"show": true,
}

var treePreservingGitSubcommands = map[string]bool{
	"fetch":     true,
	"push":      true,
	"ls-remote": true,
	"remote":    true,
	"tag":       true,
	"rev-parse": true,
	"blame":     true,
	"ls-files":  true,
	"describe":  true,
	"cat-file":  true,
	"config":    true,
	"reflog":    true,
	"add":       true,
	"commit":    true,
	"init":      true,
	"clone":     true,
	"fsck":      true,
}

var treePreservingWorktreeSubs = map[string]bool{
	"list": true,
	"add":  true,
}

func wtArgFields(argsJSON string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return nil
	}
	return m
}

var gitBranchMutatingFlags = map[string]bool{
	"-d": true, "-D": true, "--delete": true, "-m": true, "--move": true,
}

func isShellBoundaryToken(tok string) bool {
	switch tok {
	case "&&", "||", ";", "|":
		return true
	}
	return false
}

func classifyGitCommandLine(line string) (readOnly, found bool) {
	fields := strings.Fields(line)
	readOnly = true
	for idx := 0; idx < len(fields); idx++ {
		if filepath.Base(fields[idx]) != "git" || idx+1 >= len(fields) {
			continue
		}

		subIdx := skipGitGlobalFlags(fields, idx+1)
		if subIdx >= len(fields) {
			return false, true
		}
		sub := strings.ToLower(fields[subIdx])
		switch {
		case sub == "stash":
			found = true
			if subIdx+1 >= len(fields) {
				return false, true
			}
			if !readOnlyStashActions[strings.ToLower(fields[subIdx+1])] {
				readOnly = false
			}
		case sub == "branch":

			found = true

			for _, f := range fields[subIdx+1:] {
				if isShellBoundaryToken(f) {
					break
				}
				if gitBranchMutatingFlags[f] {
					readOnly = false
				}
			}
		case sub == "worktree":

			found = true
			if subIdx+1 >= len(fields) || !treePreservingWorktreeSubs[strings.ToLower(fields[subIdx+1])] {
				readOnly = false
			}
		case sub == "checkout" || sub == "switch":

			found = true
			creating := false
			positionals := 0
			for _, f := range fields[subIdx+1:] {
				if isShellBoundaryToken(f) || f == "--" {
					break
				}
				if f == "-b" || f == "-B" || (sub == "switch" && (f == "-c" || f == "-C")) {
					creating = true
					continue
				}
				if strings.HasPrefix(f, "-") {
					continue
				}
				positionals++
			}
			if !creating || positionals > 1 {
				readOnly = false
			}
		case sub == "reset":

			found = true
			softMixed := true
			sawBoundary := false
			for _, f := range fields[subIdx+1:] {
				if f == "--" {
					sawBoundary = true
					break
				}
				if isShellBoundaryToken(f) {
					break
				}
				if f == "--hard" {
					softMixed = false
				}
			}
			if !softMixed || sawBoundary {
				readOnly = false
			}
		case readOnlyGitSubcommands[sub], treePreservingGitSubcommands[sub]:
			found = true
		default:
			return false, true
		}
	}
	return readOnly, found
}

func skipGitGlobalFlags(fields []string, start int) int {
	i := start
	for i < len(fields) {
		f := fields[i]
		switch {
		case f == "-C" || f == "-c" || f == "--git-dir" || f == "--work-tree":
			i += 2
		case f == "--no-pager" || f == "--no-optional-locks" || f == "--literal-pathspecs":
			i++
		case f == "--no-replace-objects" || f == "--bare" || f == "--paginate" || f == "--no-paginate":
			i++
		case strings.HasPrefix(f, "--git-dir=") || strings.HasPrefix(f, "--work-tree=") || strings.HasPrefix(f, "-C") && f != "-C":
			i++
		default:
			return i
		}
	}
	return i
}

func isReadOnlyGitInvocation(toolName, argsJSON string) bool {
	m := wtArgFields(argsJSON)
	if m == nil {
		return false
	}

	anyFound := false
	for k, v := range m {
		if k == "description" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if readOnly, found := classifyGitCommandLine(s); found {
			anyFound = true
			if !readOnly {
				return false
			}
		}
	}
	if anyFound {
		return true
	}

	if toolName == "git_stash" {
		if action, ok := m["action"].(string); ok {
			return readOnlyStashActions[strings.ToLower(strings.TrimSpace(action))]
		}
	}

	if toolName == "git_checkout" {
		if create, ok := m["create"].(bool); ok && create {
			if sp, ok := m["start_point"].(string); ok && sp != "" {
				return false
			}
			return true
		}
		return false
	}

	if toolName == "git_reset" {
		if mode, ok := m["mode"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(mode)) {
			case "soft", "mixed":
				return true
			}
		}
		return false
	}
	return false
}

func runCommandMutatesTree(argsJSON string) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return false
	}
	cmd, _ := m["command"].(string)
	if cmd == "" {
		return false
	}
	readOnly, found := classifyGitCommandLine(cmd)
	return found && !readOnly
}

func (w *wtInvalidationState) checkMutation(toolName string, argsJSON string) string {

	if isReadOnlyGitInvocation(toolName, argsJSON) {
		return ""
	}
	if w.warnedCount >= maxWTInvalidationWarnings {
		return ""
	}
	if len(w.readFiles) < minReadsBeforeWarning {
		return ""
	}

	if w.readsSinceWarning == 0 {
		return ""
	}

	w.warnedCount++
	w.readsSinceWarning = 0

	var paths []string
	count := 0
	for p := range w.readFiles {
		if count >= 3 {
			break
		}
		paths = append(paths, p)
		count++
	}

	suffix := ""
	if len(w.readFiles) > 3 {
		suffix = fmt.Sprintf(" and %d more", len(w.readFiles)-3)
	}

	debug.Log("wt_invalidation", "git mutation %s invalidated %d cached reads", toolName, len(w.readFiles))

	toolList := strings.Join(paths, ", ")
	return fmt.Sprintf(
		"[Working-Tree Invalidation] The tool %s may have changed files on disk. "+
			"Your cached reads of %d file(s) are now potentially stale: %s%s. "+
			"Re-read files before editing to avoid operating on outdated content.",
		toolName, len(w.readFiles), toolList, suffix,
	)
}

func normalizeWTPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	path = strings.TrimPrefix(path, "./")
	return path
}
