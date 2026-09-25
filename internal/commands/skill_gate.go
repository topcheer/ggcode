package commands

// Conditional skill activation via the `paths` frontmatter key.
//
// Research basis (r95 frontier sync): "Harness Engineering: Anatomy,
// Architecture, and Evolution of Coding Agents" (arXiv:2609.00006, §12.5 and
// Observation 8) documents conditional activation — Claude Code's `paths`
// frontmatter, OpenHands' PathTrigger rules, OpenClaw's `requires` — as the
// advance that "pushes JIT context engineering into the extensibility layer",
// and notes it as a structural improvement over MCP's always-on tool
// exposure. ggcode already gated skills on external CLI tools
// (RequiresTools), but a SKILL.md declaring `paths:` had the key silently
// dropped by the YAML loader, so Claude Code-style bundles lost their
// activation condition when used with ggcode.
//
// Semantics — discovery gating only, never conversation injection:
//   - A skill with non-empty Paths is hidden from model-facing discovery
//     surfaces (Manager.SkillNames → skill '?search' and fuzzy suggestions,
//     and the system-prompt skill list) until the agent has successfully
//     touched (read/edit/write) a file matching at least one declared glob.
//   - Activation is monotonic and process-scoped: once matched, the skill
//     stays visible for the rest of the session.
//   - Direct invocation (skill tool with an exact name, /slash by the user)
//     is never blocked: gating filters discovery, not execution.
//   - No text is injected into the conversation anywhere on this path. The
//     only effect is filtering of existing surfaces, so it adds no new
//     content-source/timing/injection-surface cell (r94 3D table).

import (
	"path/filepath"
	"strings"
	"sync"
)

type skillGateState struct {
	mu        sync.Mutex
	patterns  map[string][]string // skill name -> declared globs
	activated map[string]struct{} // skills activated this session
	touched   map[string]struct{} // normalized touched paths
}

var skillGate = &skillGateState{
	patterns:  make(map[string][]string),
	activated: make(map[string]struct{}),
	touched:   make(map[string]struct{}),
}

// NoteTouchedPaths records file paths the agent successfully touched
// (read/edit/write) and activates any registered path-gated skill whose
// globs match. Called from the agent loop after successful tool execution;
// a cheap no-op while no path-gated skills are registered.
func NoteTouchedPaths(paths ...string) {
	if len(paths) == 0 {
		return
	}
	skillGate.mu.Lock()
	defer skillGate.mu.Unlock()
	for _, p := range paths {
		n := normalizeGlobPath(p)
		if n == "" {
			continue
		}
		skillGate.touched[n] = struct{}{}
		if len(skillGate.patterns) == 0 {
			continue // nothing registered yet; keep the touch for later registrations
		}
		for name, pats := range skillGate.patterns {
			if _, done := skillGate.activated[name]; done {
				continue
			}
			if pathGlobsMatch(pats, n) {
				skillGate.activated[name] = struct{}{}
			}
		}
	}
}

// registerGatedSkill records (or updates) a skill's activation globs. An
// empty pattern list unregisters. Newly registered globs are evaluated
// against paths already touched this session, so a hot-reload that
// (re)introduces a gated skill activates immediately if the condition was
// already met.
func registerGatedSkill(name string, patterns []string) {
	skillGate.mu.Lock()
	defer skillGate.mu.Unlock()
	if len(patterns) == 0 {
		delete(skillGate.patterns, name)
		delete(skillGate.activated, name)
		return
	}
	skillGate.patterns[name] = patterns
	if _, done := skillGate.activated[name]; !done {
		for n := range skillGate.touched {
			if pathGlobsMatch(patterns, n) {
				skillGate.activated[name] = struct{}{}
				break
			}
		}
	}
}

func unregisterGatedSkill(name string) {
	skillGate.mu.Lock()
	defer skillGate.mu.Unlock()
	if _, ok := skillGate.patterns[name]; ok {
		delete(skillGate.patterns, name)
		delete(skillGate.activated, name)
	}
}

// SkillHiddenByPaths reports whether a path-gated skill is still hidden from
// model-facing discovery because no matching file has been touched yet.
// Skills without registered globs are never hidden.
func SkillHiddenByPaths(name string) bool {
	skillGate.mu.Lock()
	defer skillGate.mu.Unlock()
	if _, ok := skillGate.patterns[name]; !ok {
		return false
	}
	_, done := skillGate.activated[name]
	return !done
}

// ResetSkillGateForTest clears all gate state. Not for production use.
func ResetSkillGateForTest() {
	skillGate.mu.Lock()
	defer skillGate.mu.Unlock()
	skillGate.patterns = make(map[string][]string)
	skillGate.activated = make(map[string]struct{})
	skillGate.touched = make(map[string]struct{})
}

// pathGlobsMatch reports whether any pattern matches the normalized path.
func pathGlobsMatch(patterns []string, path string) bool {
	for _, p := range patterns {
		if matchSkillPathGlob(p, path) {
			return true
		}
	}
	return false
}

// normalizeGlobPath normalizes a path for glob comparison.
func normalizeGlobPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return filepath.Clean(p)
}

// matchSkillPathGlob matches one glob against one normalized path. Supported
// forms (gitignore-flavored, following Claude Code's `paths` semantics):
//
//	**/*.ts              any depth
//	src/**               everything under src/
//	internal/tool/*.go   single-segment wildcards
//	Makefile             exact path
//	*.md                 slash-less patterns also match any base name
//
// An absolute touched path is additionally matched against its bounded
// suffixes so a relative pattern like "src/**/*.ts" matches /repo/src/a/b.ts.
func matchSkillPathGlob(pattern, path string) bool {
	pattern = normalizeGlobPath(pattern)
	if pattern == "" || path == "" {
		return false
	}
	if !strings.Contains(pattern, "**") {
		if ok, err := filepath.Match(pattern, path); err == nil && ok {
			return true
		}
		if !strings.ContainsAny(pattern, "*?[") {
			// Exact path: no base-name fallback for wildcard-free patterns.
			return false
		}
		if !strings.Contains(pattern, "/") {
			ok, _ := filepath.Match(pattern, filepath.Base(path))
			return ok
		}
	}
	patSegs := strings.Split(pattern, "/")
	for _, candidate := range globPathSuffixes(path) {
		if matchGlobSegments(patSegs, strings.Split(candidate, "/")) {
			return true
		}
	}
	return false
}

// globPathSuffixes yields the path and its suffixes starting at each segment
// boundary (bounded) so repo-relative globs can match absolute paths.
func globPathSuffixes(path string) []string {
	segs := strings.Split(path, "/")
	limit := len(segs)
	if limit > 7 {
		limit = 7
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, strings.Join(segs[i:], "/"))
	}
	return out
}

// matchGlobSegments matches '**'-aware segment lists. filepath.Match handles
// per-segment wildcards; '**' consumes zero or more segments.
func matchGlobSegments(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(seg); i++ {
				if matchGlobSegments(rest, seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		ok, err := filepath.Match(pat[0], seg[0])
		if err != nil || !ok {
			return false
		}
		pat = pat[1:]
		seg = seg[1:]
	}
	return len(seg) == 0
}
