package memory

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Environment-probing curation (arXiv:2609.11060, "Grounding Agent Memory:
// Environment-Probing Curation for Enterprise Agents"): a post-task curator
// restricted to completed trajectories preserves errors, overgeneralizes
// partial evidence, and retains stale knowledge. These probes give the
// memory curator least-privilege, READ-ONLY access to the workspace so that
// code identifiers referenced by memory entries can be verified against the
// codebase's current reality instead of being trusted blindly.
//
// Probing is heuristic by design: a missing identifier is an advisory
// staleness signal ("broken-symbol"), never an automatic delete.

// symbolRefPattern extracts backticked identifiers from memory content,
// e.g. `ScanStaleness` inside "extend `ScanStaleness` with ...". Backticks
// are the agent's own quoting convention for code, which keeps precision
// high compared to scanning prose.
var symbolRefPattern = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]{5,63})`")

const (
	// maxProbeIdentsPerEntry caps identifier extraction per memory entry
	// so one verbose entry cannot dominate a scan.
	maxProbeIdentsPerEntry = 12
	// maxProbeFileBytes caps the size of a single file read.
	maxProbeFileBytes = 1 << 20 // 1MB
)

// Workspace-scan caps are vars so tests can inject tiny values and force
// the truncation paths (#2621). Production code must not mutate them.
var (
	// maxProbeFiles bounds the workspace walk per scan.
	maxProbeFiles = 20000
	// maxProbeTotalBytes bounds the total bytes read per scan.
	maxProbeTotalBytes int64 = 48 << 20 // 48MB
)

// probeCodeExts lists file extensions eligible for identifier probing.
// Only code files count: docs and notes keep referencing deleted symbols
// long after the code is gone, so they must not mask a stale claim.
var probeCodeExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".mjs": true,
	".cjs": true, ".py": true, ".rs": true, ".java": true, ".kt": true,
	".rb": true, ".c": true, ".h": true, ".cc": true, ".cpp": true,
	".hpp": true, ".cs": true, ".swift": true, ".php": true, ".dart": true,
}

// skipProbeDirs lists directory names never descended into during a probe.
var skipProbeDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".ggcode": true,
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, "__pycache__": true, ".venv": true, "venv": true,
	"coverage": true, ".idea": true, ".vscode": true,
}

// probeCandidate is a memory entry pending symbol verification.
type probeCandidate struct {
	key    string
	age    time.Duration
	idents []string
}

// isProbeableIdent reports whether s looks like a concrete code identifier
// worth probing. Requires camelCase, snake_case, or an embedded digit to
// filter out ordinary capitalized English words (String, Error, Window).
func isProbeableIdent(s string) bool {
	if len(s) < 6 || len(s) > 64 {
		return false
	}
	camel, snake, digit, hasLetter := false, false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= 'a' && c <= 'z':
			hasLetter = true
		case c == '_':
			snake = true
		case c >= '0' && c <= '9':
			digit = true
		default:
			return false
		}
		if i > 0 && s[i-1] >= 'a' && s[i-1] <= 'z' && c >= 'A' && c <= 'Z' {
			camel = true
		}
	}
	if !hasLetter {
		return false
	}
	return camel || snake || digit
}

// extractSymbolCandidates returns deduplicated probeable identifiers
// mentioned in backticks within content, capped at maxProbeIdentsPerEntry.
func extractSymbolCandidates(content string) []string {
	matches := symbolRefPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	idents := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		id := m[1]
		if seen[id] || !isProbeableIdent(id) {
			continue
		}
		seen[id] = true
		idents = append(idents, id)
		if len(idents) >= maxProbeIdentsPerEntry {
			break
		}
	}
	return idents
}

// identInFile reports whether ident occurs in content on identifier
// boundaries (not as a substring of a longer identifier).
func identInFile(content, ident string) bool {
	for i := 0; i+len(ident) <= len(content); {
		idx := strings.Index(content[i:], ident)
		if idx < 0 {
			return false
		}
		abs := i + idx
		before := abs == 0 || !isIdentByte(content[abs-1])
		after := abs+len(ident) >= len(content) || !isIdentByte(content[abs+len(ident)])
		if before && after {
			return true
		}
		i = abs + 1
	}
	return false
}

// isIdentByte reports whether c can appear inside a code identifier.
func isIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// probeWorkspaceIdents scans the workspace under workingDir (read-only,
// bounded) and reports which of the wanted identifiers were found. The
// scan stops early once every identifier is found or a cap is hit.
// filesScanned is returned for diagnostics. truncated reports whether a
// cap stopped the walk before the whole workspace was examined; when it
// is true, a missing identifier means "unverified", not "absent" (#2621).
func probeWorkspaceIdents(workingDir string, wanted map[string]struct{}) (found map[string]bool, filesScanned int, truncated bool) {
	found = make(map[string]bool, len(wanted))
	if len(wanted) == 0 || workingDir == "" {
		return found, 0, false
	}

	var walked int
	var totalBytes int64
	_ = filepath.WalkDir(workingDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			if path != workingDir && skipProbeDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if walked >= maxProbeFiles {
			debug.Log("memory", "symbol probe: file cap hit (%d files)", walked)
			truncated = true
			return fs.SkipAll
		}
		if !probeCodeExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > maxProbeFileBytes {
			return nil
		}
		totalBytes += info.Size()
		if totalBytes > maxProbeTotalBytes {
			debug.Log("memory", "symbol probe: total byte cap hit at %s (%d files)", path, walked)
			truncated = true
			return fs.SkipAll
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		walked++
		content := string(data)
		for id := range wanted {
			if !found[id] && identInFile(content, id) {
				found[id] = true
			}
		}
		if len(found) == len(wanted) {
			return fs.SkipAll
		}
		return nil
	})

	debug.Log("memory", "symbol probe: %d/%d identifiers found in %d files under %s (truncated=%v)",
		len(found), len(wanted), walked, workingDir, truncated)
	return found, walked, truncated
}
