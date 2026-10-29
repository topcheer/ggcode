package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Convention-file drift scan (r354; deterministic staleness catching for
// AGENTS.md/GGCODE.md, per the 2026 "catch convention-file drift without
// another LLM call" pattern): a project's bootstrap documents state how to
// build/test the project (fenced `make X` blocks, `bash scripts/foo.sh`).
// After a refactor those claims silently rot — the agent re-reads stale
// instructions every session and follows them into failures.
//
// ScanConventionDrift extracts only claims that can be verified with
// certainty (make targets against the Makefile, script paths against the
// filesystem) from FENCED code blocks only — inline prose is skipped to
// keep false positives near zero. Zero LLM cost.

// conventionDriftCap bounds how many drift findings are reported.
const conventionDriftCap = 5

// makeTargetLine matches a fenced-block line invoking a make target,
// e.g. "make verify-ci" or "make -C mobile build" (the -C form is skipped:
// the target lives in another Makefile we do not parse here).
var makeTargetLine = regexp.MustCompile(`^make\s+([a-zA-Z0-9_.-]+)\s*$`)

// scriptLine matches a fenced-block line invoking a shell script by path,
// e.g. "bash scripts/version_sync.sh 1.2.3" / "sh ./tools/run.sh".
var scriptLine = regexp.MustCompile(`^(?:bash|sh)\s+(\.?/?[\w./-]+\.(?:sh|bash))`)

// makefileTargetLine matches "target:" definitions in a Makefile, including
// no-dependency forms ("build:" at end of line); ":=" assignments excluded.
var makefileTargetLine = regexp.MustCompile(`^([a-zA-Z0-9_.%-]+)\s*:(?:[^=].*)?$`)

// ScanConventionDrift checks the project-memory content against the working
// directory: every make target named in a fenced block must exist in the
// Makefile, every script path must exist on disk. Returns human-readable
// findings (empty when nothing drifted, no Makefile present, or content is
// empty). Pure I/O, deterministic, never fails hard.
func ScanConventionDrift(content, workingDir string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	var findings []string
	seen := make(map[string]bool)
	add := func(f string) {
		if !seen[f] && len(findings) < conventionDriftCap {
			seen[f] = true
			findings = append(findings, f)
		}
	}

	targets := makefileTargets(filepath.Join(workingDir, "Makefile"))
	// #3049-C2: an include-only Makefile (just "include foo.mk" - the
	// mobile/flutter shape) parses to a NON-nil EMPTY target map; treating
	// "map exists" as "Makefile defines targets" flagged every fenced-block
	// make invocation as referencing an undefined target. Only a Makefile
	// that actually defines targets constrains the drift scan.
	hasMakefile := len(targets) > 0

	for _, block := range fencedBlocks(content) {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if m := makeTargetLine.FindStringSubmatch(line); m != nil && hasMakefile {
				if !targets[m[1]] {
					add("make target \"" + m[1] + "\" is referenced but not defined in Makefile")
				}
				continue
			}
			if m := scriptLine.FindStringSubmatch(line); m != nil {
				p := m[1]
				if !filepath.IsAbs(p) {
					p = filepath.Join(workingDir, p)
				}
				if _, err := os.Stat(p); err != nil {
					add("script \"" + m[1] + "\" does not exist")
				}
			}
		}
	}
	return findings
}

// fencedBlocks returns the body of every ``` fenced block, skipping the
// language tag line. Tolerates unterminated blocks.
func fencedBlocks(content string) []string {
	var blocks []string
	var cur []string
	in := false
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if in {
				blocks = append(blocks, strings.Join(cur, "\n"))
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, line)
		}
	}
	if in && len(cur) > 0 {
		blocks = append(blocks, strings.Join(cur, "\n"))
	}
	return blocks
}

// makefileTargets parses target definitions; returns nil when the Makefile
// is absent (which disables make-target verification entirely — a missing
// Makefile must not flag every referenced target).
func makefileTargets(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	targets := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		if m := makefileTargetLine.FindStringSubmatch(line); m != nil {
			// #3825: `.PHONY`, `.DEFAULT_GOAL` etc. are special make
			// directives, not user targets. Capturing them let a bare
			// `include core.mk` + `.PHONY: build` root Makefile register
			// ".PHONY" and pass the hasMakefile gate, while the REAL targets
			// (defined in the included .mk) stayed unregistered - the
			// #3049-C2 empty-map guard never fired and every referenced
			// target false-flagged as undefined. Skip dot/percent-prefixed
			// directive names entirely.
			if strings.HasPrefix(m[1], ".") || strings.HasPrefix(m[1], "%") {
				continue
			}
			targets[m[1]] = true
		}
	}
	return targets
}
